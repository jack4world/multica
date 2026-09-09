package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// The audit review gate: the enforcement half of the three-level workpaper
// review chain that internal/auditmode seeds.
//
// WHERE IT RUNS. Inside the two helpers every USER-DRIVEN issue write funnels
// through — runWithIssueStatusGuard and updateIssueAtomically — plus the create
// path, rather than in the handlers. The single and batch endpoints each choose
// between those two helpers depending on whether the request also carries text,
// so gating the helpers covers all four combinations at once and means a future
// endpoint inherits the control instead of having to remember it. Gating the
// handlers instead would leave the batch path free to drift, which is the
// single most likely way a control like this fails.
//
// WHAT IT DOES NOT COVER. Three writers change issue.status without a user and
// do NOT pass through here: the GitHub PR-merge path (handler/github.go), the
// stuck-issue sweeper (service/task.go, which resets in_progress — the category
// 编制中 carries), and the runtime sweeper (cmd/server/runtime_sweeper.go).
// Each would make a transition this gate refuses. They are out of scope
// deliberately: changing them affects every workspace on the platform, which is
// a different blast radius from a gate that is inert outside auditee
// workspaces. Do not read the paragraph above as "nothing can move a status
// without asking" — that is true of the API, not of the system.
//
// WHAT IT COSTS EVERYONE ELSE. Nothing. reviewGate.mayApply answers from status
// keys alone: unless an audit status is on one side of the write and the issue
// belongs to an engagement, the gate returns without a single query.

// reviewGate is what one issue write knows about itself before the gate has
// looked anything up. A nil *reviewGate means the caller is not a status- or
// content-write on an issue and there is nothing to govern.
type reviewGate struct {
	// prev is the issue as it stands, supplying its current status and whether
	// it belongs to an engagement.
	prev db.Issue
	// target is the resolved target status key, or "" when the write leaves
	// status alone. A content edit is still a write a filed workpaper refuses.
	target string
	// targetProject is the issue's project AFTER the write. Callers that cannot
	// move an issue between projects pass prev.ProjectID.
	targetProject pgtype.UUID
	// actorType is "member" or "agent", as resolveActor classifies it.
	actorType string
	// actorID is the acting user's id, or the agent's.
	actorID string
	// reason is the free text accompanying the write. A rejection needs one;
	// everything else ignores it.
	reason string
	// recordedTrail reports whether the gate wrote a trail entry for this
	// write. The caller passes it to the activity listener so the timeline does
	// not show the same change twice.
	recordedTrail bool
	// pendingEntry holds the row written inside the transaction, for the caller
	// to announce once it has committed.
	pendingEntry *db.ActivityLog
}

// strPtrValue reads an optional request string.
func strPtrValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// mayApply reports whether this write could be governed, using no database at
// all. Two facts settle it: a workpaper is an issue that belongs to an
// engagement, and the chain is only involved when an audit status is on one
// side of the transition.
func (g *reviewGate) mayApply() bool {
	if g == nil {
		return false
	}
	if !g.prev.ProjectID.Valid && !g.targetProject.Valid {
		return false
	}
	// A write that moves an issue between projects is always worth deciding:
	// engagement membership is what makes an issue a workpaper, so changing it
	// can carry a workpaper out of the gate's reach or smuggle one in.
	if g.projectChanged() {
		return true
	}
	return auditgate.Governs(g.prev.Status, g.target)
}

func (g *reviewGate) projectChanged() bool {
	return g.prev.ProjectID != g.targetProject
}

// reviewGateDenial carries a refusal from the gate back up through the write
// helpers, which know nothing about audit rules, to the handler that renders it.
type reviewGateDenial struct {
	decision auditgate.Decision
}

func (e *reviewGateDenial) Error() string { return e.decision.Reason }

// writeReviewGateError renders a gate refusal and reports whether it handled
// the error.
//
// Two shapes, two statuses. A refusal about WHO is acting is 403: the request
// describes a legal transition that this person may not make. A refusal about
// WHAT is being asked is 409: the transition itself does not exist, or the
// workpaper is filed, and no change of identity would help.
func writeReviewGateError(w http.ResponseWriter, err error) bool {
	var denial *reviewGateDenial
	if !errors.As(err, &denial) {
		return false
	}
	status := http.StatusConflict
	switch denial.decision.Code {
	case auditgate.DenyLevelRequired, auditgate.DenySelfReview, auditgate.DenyAgent:
		status = http.StatusForbidden
	}
	writeError(w, status, denial.decision.Reason)
	return true
}

// enforceReviewGate decides one issue write and, when the decision says so,
// records the preparer as part of the same transaction — so a workpaper can
// never reach review with no preparer on record.
//
// q is the caller's transactional handle: every read the decision rests on and
// the write it authorizes commit or roll back together.
func (h *Handler) enforceReviewGate(ctx context.Context, q *db.Queries, g *reviewGate) error {
	decision, actingMemberID, decided, applies, err := h.decideReviewGate(ctx, q, g, true)
	if err != nil {
		return err
	}
	if !applies {
		return nil
	}
	if !decision.Allowed {
		return &reviewGateDenial{decision: decision}
	}
	if decision.RecordPreparer {
		if err := q.RecordWorkpaperPreparer(ctx, db.RecordWorkpaperPreparerParams{
			IssueID:     g.prev.ID,
			WorkspaceID: g.prev.WorkspaceID,
			PreparerID:  actingMemberID,
		}); err != nil {
			return err
		}
	}
	if err := h.recordAuditTrail(ctx, q, g, decided, decision); err != nil {
		return err
	}
	g.recordedTrail = decision.Event != ""
	return nil
}

// recordAuditTrail writes the trail entry for a transition, in the SAME
// transaction as the change it describes.
//
// This is the whole point of putting it here. The platform's activity entries
// are written by an event-bus listener that runs after the write has committed
// and swallows its own failures — fine for a product timeline, fatal for an
// audit record, because a workpaper could be filed with nothing saying so and
// nobody told. Here the record and the change share a fate: a failure to write
// the trail fails the transition.
func (h *Handler) recordAuditTrail(ctx context.Context, q *db.Queries, g *reviewGate, decided db.Issue, decision auditgate.Decision) error {
	if decision.Event == "" {
		return nil
	}
	// `decided` is the row the decision was made on — re-read under the row
	// lock — not the snapshot the handler loaded before the transaction opened.
	// Recording from the snapshot would let a concurrent write make the entry
	// name a transition that never happened, in the record this whole change
	// exists to make trustworthy.
	details := map[string]any{
		"from": decided.Status,
		"to":   g.target,
	}
	if decision.RecordLevel != "" {
		details["level"] = string(decision.RecordLevel)
	}
	if reason := strings.TrimSpace(g.reason); reason != "" {
		details["reason"] = reason
	}
	// The preparer travels with every entry, not only the submission, so a
	// reader checking independence does not have to scan backwards for it.
	if preparer, err := q.GetWorkpaperPreparer(ctx, decided.ID); err == nil {
		details["preparer_id"] = util.UUIDToString(preparer)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	// The USER id, not the member id. Every other writer of activity_log stores
	// the user id, and the timeline resolves member actors through it — writing
	// a member id here would render every audit entry with no name, and would
	// put the exported actor_id in a different namespace from every other
	// exported row, so an auditor could not tell who approved a workpaper.
	//
	// A malformed actor id is an error, not a shrug: an entry that commits with
	// a zero actor is a hole in exactly the attribution the trail is for.
	actorID, err := util.ParseUUID(g.actorID)
	if err != nil {
		return fmt.Errorf("audit trail: unusable actor id %q: %w", g.actorID, err)
	}
	entry, err := q.CreateActivity(ctx, db.CreateActivityParams{
		ID:          dbid.NewV7(),
		WorkspaceID: decided.WorkspaceID,
		IssueID:     decided.ID,
		ActorType:   pgtype.Text{String: g.actorType, Valid: g.actorType != ""},
		ActorID:     actorID,
		Action:      string(decision.Event),
		Details:     encoded,
	})
	if err != nil {
		return err
	}
	// Held, not published: this runs inside the write transaction, and an event
	// announcing a change that then rolls back is worse than a late one. The
	// caller publishes after the commit.
	g.pendingEntry = &entry
	return nil
}

// publishAuditTrailEntry announces a committed trail entry so an open issue
// view shows the review step without waiting for a refetch.
//
// Called AFTER the transaction commits. The platform's own listener no longer
// publishes for these transitions — the gate told it to stand down so the
// timeline does not show the same step twice — so without this the live update
// for exactly the transitions this feature is about would be the one thing that
// stopped working.
func (h *Handler) publishAuditTrailEntry(workspaceID string, g *reviewGate) {
	if g == nil || g.pendingEntry == nil {
		return
	}
	entry := g.pendingEntry
	g.pendingEntry = nil
	h.publish(protocol.EventActivityCreated, workspaceID, g.actorType, g.actorID, map[string]any{
		"issue_id": util.UUIDToString(entry.IssueID),
		"entry": map[string]any{
			"type":       "activity",
			"id":         util.UUIDToString(entry.ID),
			"actor_type": entry.ActorType.String,
			"actor_id":   util.UUIDToString(entry.ActorID),
			"action":     entry.Action,
			"details":    json.RawMessage(entry.Details),
			"created_at": util.TimestampToString(entry.CreatedAt),
		},
	})
}

// decideReviewGate answers one write WITHOUT changing anything, so the batch
// endpoint can pre-flight every item before it writes the first.
//
// applies is false when the gate has nothing to say, which is the case for
// every issue write outside an auditee engagement.
// lockRow must be true whenever q is transactional and the decision authorizes
// a write. The batch preflight passes false: it decides nothing on its own and
// taking row locks it immediately releases would be churn for no guarantee.
func (h *Handler) decideReviewGate(ctx context.Context, q *db.Queries, g *reviewGate, lockRow bool) (decision auditgate.Decision, actingMemberID pgtype.UUID, decided db.Issue, applies bool, err error) {
	decided = g.prev
	if !g.mayApply() {
		return auditgate.Decision{}, pgtype.UUID{}, decided, false, nil
	}

	// A workspace that never enabled audit mode can still hold a hand-made
	// status keyed `review_l1`, so the key check above is necessary but not
	// sufficient. This is the first query the gate makes, and it is only
	// reached for issues actually sitting on, or moving to, an audit status.
	enabled, err := q.IsWorkspaceAuditMode(ctx, g.prev.WorkspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auditgate.Decision{}, pgtype.UUID{}, decided, false, nil
		}
		return auditgate.Decision{}, pgtype.UUID{}, decided, false, err
	}
	if !enabled {
		return auditgate.Decision{}, pgtype.UUID{}, decided, false, nil
	}

	// Re-read the issue under a row lock, inside this transaction. g.prev came
	// from a snapshot taken before the transaction opened, and deciding from it
	// makes filed-immutability only as true as the absence of a concurrent
	// filer. A create has no row to lock and keeps its synthetic prev.
	//
	// Residual, and deliberate: an issue that is NOT in the chain and whose
	// write does not move it between projects never opens a transaction at all,
	// so an issue entering the chain concurrently can still be written by a
	// request that saw it outside. Closing that would mean locking the row on
	// every issue write on the platform, which is the cost this gate exists to
	// avoid paying.
	if lockRow && g.prev.ID.Valid {
		locked, lockErr := q.LockIssueForReviewGate(ctx, db.LockIssueForReviewGateParams{
			ID:          g.prev.ID,
			WorkspaceID: g.prev.WorkspaceID,
		})
		if lockErr != nil && !errors.Is(lockErr, pgx.ErrNoRows) {
			return auditgate.Decision{}, pgtype.UUID{}, decided, false, lockErr
		}
		if lockErr == nil {
			decided = locked
			g = &reviewGate{
				prev: locked, target: g.target, targetProject: g.targetProject,
				actorType: g.actorType, actorID: g.actorID, reason: g.reason,
			}
		}
	}

	in := auditgate.Input{
		InEngagement:       g.prev.ProjectID.Valid,
		TargetInEngagement: g.targetProject.Valid,
		ProjectChanged:     g.projectChanged(),
		From:               g.prev.Status,
		To:                 g.target,
		Reason:             g.reason,
		ActorIsAgent:       g.actorType == "agent",
	}

	if !in.ActorIsAgent {
		// Read through q, not h.Queries: the actor's membership and admin status
		// are decision inputs, and reading them from a different snapshot than
		// the role and preparer rows would break the invariant this whole
		// function rests on.
		actorUUID, parseErr := util.ParseUUID(g.actorID)
		if parseErr != nil {
			return auditgate.Decision{
				Code:   auditgate.DenyLevelRequired,
				Reason: "you are not a member of this auditee and cannot move its workpapers",
			}, pgtype.UUID{}, decided, true, nil
		}
		member, memberErr := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID:      actorUUID,
			WorkspaceID: g.prev.WorkspaceID,
		})
		if memberErr != nil {
			// Not a member of the auditee: the caller reached this issue some
			// other way, and holds no standing in its review chain.
			if errors.Is(memberErr, pgx.ErrNoRows) {
				return auditgate.Decision{
					Code:   auditgate.DenyLevelRequired,
					Reason: "you are not a member of this auditee and cannot move its workpapers",
				}, pgtype.UUID{}, decided, true, nil
			}
			return auditgate.Decision{}, pgtype.UUID{}, decided, false, memberErr
		}
		actingMemberID = member.ID
		in.ActorMemberID = util.UUIDToString(member.ID)
		in.ActorIsAdmin = member.Role == "owner" || member.Role == "admin"

		// Ranks are scoped to an engagement. Read them from the one the issue
		// is in now; a write that moves it between engagements is refused
		// outright while it carries a chain status, so there is no case where
		// the destination's ranks would be the ones to consult.
		roleProject := g.prev.ProjectID
		if !roleProject.Valid {
			roleProject = g.targetProject
		}
		level, levelErr := q.GetAuditRoleLevel(ctx, db.GetAuditRoleLevelParams{
			ProjectID: roleProject,
			MemberID:  member.ID,
		})
		if levelErr != nil && !errors.Is(levelErr, pgx.ErrNoRows) {
			return auditgate.Decision{}, pgtype.UUID{}, decided, false, levelErr
		}
		if levelErr == nil {
			in.ActorLevel = auditgate.Level(level)
		}
	}

	preparer, preparerErr := q.GetWorkpaperPreparer(ctx, g.prev.ID)
	if preparerErr != nil && !errors.Is(preparerErr, pgx.ErrNoRows) {
		return auditgate.Decision{}, pgtype.UUID{}, decided, false, preparerErr
	}
	if preparerErr == nil {
		in.PreparerID = util.UUIDToString(preparer)
	}

	return auditgate.Decide(in), actingMemberID, decided, true, nil
}

// preflightBatchReviewGate runs the gate over every issue in a batch WITHOUT
// writing anything, and reports the first refusal.
//
// The batch endpoint needs this because its refusals are per-issue: one
// workpaper may be filed while the next is fine, so aborting partway would
// leave the earlier items written and still return an error — the caller's view
// of the resulting state would simply be wrong. Deciding everything up front
// means the batch either applies or does not.
//
// The authoritative check still happens inside each write's own transaction;
// this pass exists to make the batch all-or-nothing, not to replace it.
func (h *Handler) preflightBatchReviewGate(ctx context.Context, issueIDs []string, workspaceID pgtype.UUID, targetStatus, actorType, actorID, reason string) error {
	// One flag read decides the whole batch. Without it this pass would load
	// every issue in every batch on the platform just to discover, per issue,
	// that the workspace is not an auditee — and the write loop then re-reads
	// all of them.
	enabled, err := h.Queries.IsWorkspaceAuditMode(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if !enabled {
		return nil
	}

	for _, raw := range issueIDs {
		issueUUID, err := util.ParseUUID(raw)
		if err != nil {
			// Malformed ids are skipped by the write loop too; nothing to decide.
			continue
		}
		issue, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
			ID:          issueUUID,
			WorkspaceID: workspaceID,
		})
		if err != nil {
			continue
		}
		gate := &reviewGate{prev: issue, target: targetStatus, targetProject: issue.ProjectID, actorType: actorType, actorID: actorID, reason: reason}
		decision, _, _, applies, err := h.decideReviewGate(ctx, h.Queries, gate, false)
		if err != nil {
			return err
		}
		if applies && !decision.Allowed {
			return &reviewGateDenial{decision: decision}
		}
	}
	return nil
}
