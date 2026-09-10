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
	"github.com/multica-ai/multica/server/internal/remediate"
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
	return g.mayApplyReview() || remediate.Governs(g.prev.Status, g.target)
}

// mayApplyReview reports whether the REVIEW chain could govern this write. A
// remediation item belongs to no engagement, so it never satisfies this and
// falls through to the ledger's own gate.
func (g *reviewGate) mayApplyReview() bool {
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

// gateDecision is one chain's answer in the shape the write path needs. Both
// chains produce it, so the helpers that carry a refusal up to the handler
// never learn which chain refused.
//
// The HTTP status is decided by the chain that made the decision rather than by
// the renderer: each chain knows which of its own refusals are about WHO is
// acting and which are about WHAT is being asked, and a renderer switching over
// two packages' codes would be the place they silently diverge.
type gateDecision struct {
	allowed bool
	code    string
	reason  string
	status  int
	// event is what the trail records, or empty when this write is not a
	// transition worth recording.
	event string
	// recordPreparer asks the caller to snapshot the actor as the workpaper's
	// preparer as part of the same write.
	recordPreparer bool
	// recordLevel is the reviewer rank that acted, for events where one did.
	recordLevel string
	// recordVerification asks the caller to write the closure record — who
	// verified the fix, when, and what they checked — in the same transaction.
	recordVerification bool
}

// fromReviewDecision translates the review chain's answer.
//
// Two shapes, two statuses. A refusal about WHO is acting is 403: the request
// describes a legal transition that this person may not make. A refusal about
// WHAT is being asked is 409: the transition itself does not exist, or the
// workpaper is filed, and no change of identity would help.
func fromReviewDecision(d auditgate.Decision) gateDecision {
	status := http.StatusConflict
	switch d.Code {
	case auditgate.DenyLevelRequired, auditgate.DenySelfReview, auditgate.DenyAgent:
		status = http.StatusForbidden
	case auditgate.DenyReasonRequired:
		// A missing field is a bad request, not a conflict or a permission
		// problem: the caller can fix it by sending one.
		status = http.StatusBadRequest
	}
	return gateDecision{
		allowed: d.Allowed, code: string(d.Code), reason: d.Reason, status: status,
		event: string(d.Event), recordPreparer: d.RecordPreparer, recordLevel: string(d.RecordLevel),
	}
}

// fromRemediationDecision translates the ledger's answer, by the same rule.
func fromRemediationDecision(d remediate.Decision) gateDecision {
	status := http.StatusConflict
	switch d.Code {
	case remediate.DenyVerifierRequired, remediate.DenySelfVerification,
		remediate.DenyNotResponsible, remediate.DenyAgent:
		status = http.StatusForbidden
	case remediate.DenyNoteRequired:
		status = http.StatusBadRequest
	}
	return gateDecision{
		allowed: d.Allowed, code: string(d.Code), reason: d.Reason, status: status,
		event:              string(d.Event),
		recordVerification: d.Event == remediate.EventVerified,
	}
}

// reviewGateDenial carries a refusal from the gate back up through the write
// helpers, which know nothing about audit rules, to the handler that renders it.
type reviewGateDenial struct {
	decision gateDecision
}

func (e *reviewGateDenial) Error() string { return e.decision.reason }

// writeReviewGateError renders a gate refusal and reports whether it handled
// the error.
func writeReviewGateError(w http.ResponseWriter, err error) bool {
	var denial *reviewGateDenial
	if !errors.As(err, &denial) {
		return false
	}
	// The CODE, not just the sentence. The client maps it to copy in the
	// reader's language; without it a Chinese-locale auditor is shown an
	// English sentence naming a machine identifier they have never seen.
	writeErrorCode(w, denial.decision.status, denial.decision.code, denial.decision.reason)
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
	if !decision.allowed {
		return &reviewGateDenial{decision: decision}
	}
	if decision.recordPreparer {
		if err := q.RecordWorkpaperPreparer(ctx, db.RecordWorkpaperPreparerParams{
			IssueID:     g.prev.ID,
			WorkspaceID: g.prev.WorkspaceID,
			PreparerID:  actingMemberID,
		}); err != nil {
			return err
		}
	}
	// The closure record and the status that says the item is closed are one
	// write. An item that reads as closed with nobody recorded as having closed
	// it is the exact hole the ledger exists to fill.
	if decision.recordVerification {
		if err := q.RecordRemediationVerification(ctx, db.RecordRemediationVerificationParams{
			IssueID:          g.prev.ID,
			WorkspaceID:      g.prev.WorkspaceID,
			VerifiedBy:       actingMemberID,
			VerificationNote: strings.TrimSpace(g.reason),
		}); err != nil {
			return err
		}
	}
	if err := h.recordAuditTrail(ctx, q, g, decided, decision); err != nil {
		return err
	}
	g.recordedTrail = decision.event != ""
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
func (h *Handler) recordAuditTrail(ctx context.Context, q *db.Queries, g *reviewGate, decided db.Issue, decision gateDecision) error {
	if decision.event == "" {
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
	if decision.recordLevel != "" {
		details["level"] = decision.recordLevel
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
		Action:      decision.event,
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
func (h *Handler) decideReviewGate(ctx context.Context, q *db.Queries, g *reviewGate, lockRow bool) (decision gateDecision, actingMemberID pgtype.UUID, decided db.Issue, applies bool, err error) {
	decided = g.prev
	if !g.mayApply() {
		return gateDecision{}, pgtype.UUID{}, decided, false, nil
	}

	// A workspace that never enabled audit mode can still hold a hand-made
	// status keyed `review_l1`, so the key check above is necessary but not
	// sufficient. This is the first query the gate makes, and it is only
	// reached for issues actually sitting on, or moving to, an audit status.
	enabled, err := q.IsWorkspaceAuditMode(ctx, g.prev.WorkspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gateDecision{}, pgtype.UUID{}, decided, false, nil
		}
		return gateDecision{}, pgtype.UUID{}, decided, false, err
	}
	if !enabled {
		return gateDecision{}, pgtype.UUID{}, decided, false, nil
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
			return gateDecision{}, pgtype.UUID{}, decided, false, lockErr
		}
		if lockErr == nil {
			decided = locked
			g = &reviewGate{
				prev: locked, target: g.target, targetProject: g.targetProject,
				actorType: g.actorType, actorID: g.actorID, reason: g.reason,
			}
		}
	}

	// Which chain governs this write. The review chain answers first: a
	// workpaper leaving its chain for a remediation status is that chain's
	// refusal to make, and it is the one that can explain why.
	if g.mayApplyReview() {
		in, memberID, inputErr := h.auditGateInput(ctx, q, g, decided)
		if inputErr != nil {
			return gateDecision{}, pgtype.UUID{}, decided, false, inputErr
		}
		return fromReviewDecision(auditgate.Decide(in)), memberID, decided, true, nil
	}

	in, memberID, inputErr := h.remediationGateInput(ctx, q, g)
	if inputErr != nil {
		return gateDecision{}, pgtype.UUID{}, decided, false, inputErr
	}
	return fromRemediationDecision(remediate.Decide(in)), memberID, decided, true, nil
}

// remediationGateInput collects the facts a ledger decision rests on: whether
// this issue is on the ledger at all, who owes the fix, and whether the actor
// holds a rank on the engagement that raised it.
//
// Read through q, the caller's transactional handle, for the reason
// auditGateInput is: the standing being checked and the write being authorized
// have to see one snapshot.
func (h *Handler) remediationGateInput(ctx context.Context, q *db.Queries, g *reviewGate) (remediate.Input, pgtype.UUID, error) {
	var actingMemberID pgtype.UUID
	in := remediate.Input{
		From:         g.prev.Status,
		To:           g.target,
		Note:         g.reason,
		ActorIsAgent: g.actorType == "agent",
	}
	// issue.assignee_id holds the USER id for a member assignee — that is the
	// platform's contract (validateAssigneePair looks it up with
	// GetMemberByUserAndWorkspace), and it is what the actor below is compared
	// against. Reading it as a member id would make the self-verification rule
	// compare two different namespaces, which never matches: the person who
	// owes the fix would be allowed to close their own item.
	if g.prev.AssigneeType.String == "member" && g.prev.AssigneeID.Valid {
		in.ResponsibleUserID = util.UUIDToString(g.prev.AssigneeID)
	}

	record, recordErr := q.GetAuditRemediation(ctx, g.prev.ID)
	if recordErr != nil && !errors.Is(recordErr, pgx.ErrNoRows) {
		return in, pgtype.UUID{}, recordErr
	}
	if recordErr != nil {
		// Not on the ledger. The gate refuses on this alone, so nothing else is
		// worth reading.
		return in, pgtype.UUID{}, nil
	}
	in.IsRemediation = true

	if in.ActorIsAgent {
		return in, pgtype.UUID{}, nil
	}
	actorUUID, parseErr := util.ParseUUID(g.actorID)
	if parseErr != nil {
		in.RefusedActor = true
		return in, pgtype.UUID{}, nil
	}
	member, memberErr := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      actorUUID,
		WorkspaceID: g.prev.WorkspaceID,
	})
	if memberErr != nil {
		if errors.Is(memberErr, pgx.ErrNoRows) {
			in.RefusedActor = true
			return in, pgtype.UUID{}, nil
		}
		return in, pgtype.UUID{}, memberErr
	}
	actingMemberID = member.ID
	// The USER id, matching the namespace issue.assignee_id is written in.
	in.ActorUserID = util.UUIDToString(member.UserID)
	in.ActorIsAdmin = member.Role == "owner" || member.Role == "admin"

	// The rank is read from the engagement that RAISED the item, not from
	// whatever engagement the actor happens to hold a rank on. Verification is
	// the audit function's act over its own finding; a reviewer on an unrelated
	// engagement has no standing over this one.
	level, levelErr := q.GetAuditRoleLevel(ctx, db.GetAuditRoleLevelParams{
		ProjectID: record.SourceProjectID,
		MemberID:  member.ID,
	})
	if levelErr != nil && !errors.Is(levelErr, pgx.ErrNoRows) {
		return in, pgtype.UUID{}, levelErr
	}
	in.ActorIsVerifier = levelErr == nil && level != ""

	return in, actingMemberID, nil
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
		if applies && !decision.allowed {
			return &reviewGateDenial{decision: decision}
		}
	}
	return nil
}

// auditGateInput collects the facts a decision rests on: the actor's standing
// in the auditee, their rank on this engagement, and who prepared the
// workpaper.
//
// Shared by the write path and the read-only actions endpoint on purpose. The
// interface must be offered exactly what the gate would accept, and the surest
// way to guarantee that is for both to be answering questions about the same
// values rather than each assembling their own.
func (h *Handler) auditGateInput(ctx context.Context, q *db.Queries, g *reviewGate, decided db.Issue) (auditgate.Input, pgtype.UUID, error) {
	var actingMemberID pgtype.UUID
	in := auditgate.Input{
		InEngagement:       g.prev.ProjectID.Valid,
		TargetInEngagement: g.targetProject.Valid,
		ProjectChanged:     g.projectChanged(),
		From:               g.prev.Status,
		To:                 g.target,
		Reason:             g.reason,
		ActorIsAgent:       g.actorType == "agent",
	}

	// The chain's depth is a property of the engagement, and the gate cannot
	// tell a level this engagement does not run from an out-of-order step
	// without it. Read from the engagement the issue is in now; a write that
	// moves it between engagements is refused outright while it carries a
	// chain status.
	engagement := g.prev.ProjectID
	if !engagement.Valid {
		engagement = g.targetProject
	}
	if engagement.Valid {
		facts, factsErr := q.GetEngagementGateFacts(ctx, db.GetEngagementGateFactsParams{
			ID:          engagement,
			WorkspaceID: g.prev.WorkspaceID,
		})
		if factsErr != nil && !errors.Is(factsErr, pgx.ErrNoRows) {
			return in, pgtype.UUID{}, factsErr
		}
		if factsErr == nil {
			in.ReviewLevels = int(facts.ReviewLevels)
			in.EngagementArchived = facts.Archived
		}
	}

	if !in.ActorIsAgent {
		// Read through q, not h.Queries: the actor's membership and admin status
		// are decision inputs, and reading them from a different snapshot than
		// the role and preparer rows would break the invariant this whole
		// function rests on.
		actorUUID, parseErr := util.ParseUUID(g.actorID)
		if parseErr != nil {
			in.RefusedActor = true
			return in, pgtype.UUID{}, nil
		}
		member, memberErr := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID:      actorUUID,
			WorkspaceID: g.prev.WorkspaceID,
		})
		if memberErr != nil {
			// Not a member of the auditee: the caller reached this issue some
			// other way, and holds no standing in its review chain.
			if errors.Is(memberErr, pgx.ErrNoRows) {
				in.RefusedActor = true
				return in, pgtype.UUID{}, nil
			}
			return in, pgtype.UUID{}, memberErr
		}
		actingMemberID = member.ID
		in.ActorMemberID = util.UUIDToString(member.ID)
		in.ActorIsAdmin = member.Role == "owner" || member.Role == "admin"

		// Ranks are scoped to the same engagement whose depth was read above.
		level, levelErr := q.GetAuditRoleLevel(ctx, db.GetAuditRoleLevelParams{
			ProjectID: engagement,
			MemberID:  member.ID,
		})
		if levelErr != nil && !errors.Is(levelErr, pgx.ErrNoRows) {
			return in, pgtype.UUID{}, levelErr
		}
		if levelErr == nil {
			in.ActorLevel = auditgate.Level(level)
		}
	}

	// Only when the write moves the issue between projects: this is the one
	// case where being on the ledger changes the review chain's answer, and
	// paying for the read on every governed transition would buy nothing.
	if in.ProjectChanged {
		if _, err := q.GetAuditRemediation(ctx, g.prev.ID); err == nil {
			in.IsRemediationItem = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return in, pgtype.UUID{}, err
		}
	}

	preparer, preparerErr := q.GetWorkpaperPreparer(ctx, g.prev.ID)
	if preparerErr != nil && !errors.Is(preparerErr, pgx.ErrNoRows) {
		return in, pgtype.UUID{}, preparerErr
	}
	if preparerErr == nil {
		in.PreparerID = util.UUIDToString(preparer)
	}

	return in, actingMemberID, nil
}
