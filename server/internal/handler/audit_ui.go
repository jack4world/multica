package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/remediate"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The two reads the review interface needs.
//
// Neither invents a rule. The available actions come from the same matrix that
// enforces the chain, and the queue comes from the same reviewer-rank table the
// gate consults. A client that computed either for itself would be a second
// copy of the control, and two copies drift.

// AuditActionResponse is one thing the viewer may do with a workpaper.
type AuditActionResponse struct {
	Event string `json:"event"`
	To    string `json:"to"`
	// RequiresReason tells the client to collect free text before sending, so
	// the common case never reaches a refusal. What that text IS depends on the
	// event: a reviewer's reason for returning a workpaper, or the account of
	// what was fixed or checked on a remediation item.
	RequiresReason bool `json:"requires_reason"`
}

// ReviewQueueItemResponse is one workpaper waiting on the viewer.
type ReviewQueueItemResponse struct {
	Issue IssueResponse `json:"issue"`
	// Level is the rank the viewer holds on THAT engagement — different
	// engagements can put the same person at different levels.
	Level string `json:"level"`
	// WaitingSince is when the workpaper last moved, which is how long it has
	// been sitting on this reviewer.
	WaitingSince string `json:"waiting_since"`
}

// ListAuditActions returns what the current viewer may do with one workpaper.
//
// An empty list is an answer, not an error: a filed workpaper, someone else's
// level, or one's own workpaper all legitimately offer nothing, and the absence
// of buttons is the rule showing through the interface.
func (h *Handler) ListAuditActions(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := uuidToString(issue.WorkspaceID)
	actorType, actorID := h.resolveActor(r, userID, workspaceID)

	gate := &reviewGate{
		prev: issue, target: "", targetProject: issue.ProjectID,
		actorType: actorType, actorID: actorID,
	}

	// A workpaper and a remediation item are the same kind of row with
	// different rules, and one endpoint answers for both — a client should not
	// have to know which chain an issue is on to ask what it can do with it.
	if gate.mayApplyReview() {
		// Read-only: no row lock, nothing written. The authoritative check
		// still happens inside the write's own transaction when a button is
		// pressed.
		_, _, decided, applies, err := h.decideReviewGate(r.Context(), h.Queries, gate, false)
		if err != nil {
			slog.Warn("ListAuditActions failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to read audit actions")
			return
		}
		if !applies {
			writeJSON(w, http.StatusOK, []AuditActionResponse{})
			return
		}
		in, _, err := h.auditGateInput(r.Context(), h.Queries, gate, decided)
		if err != nil {
			slog.Warn("ListAuditActions input failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to read audit actions")
			return
		}
		actions := auditgate.Available(in)
		resp := make([]AuditActionResponse, 0, len(actions))
		for _, a := range actions {
			resp = append(resp, AuditActionResponse{
				Event:          string(a.Event),
				To:             a.To,
				RequiresReason: a.RequiresReason,
			})
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// The ledger. Asked directly rather than through decideReviewGate because
	// that helper answers about a TRANSITION, and an item sitting on an
	// ordinary status is one nobody has started yet — exactly the case where
	// "start remediation" is the action to offer.
	in, _, err := h.remediationGateInput(r.Context(), h.Queries, gate)
	if err != nil {
		slog.Warn("ListAuditActions ledger input failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to read audit actions")
		return
	}
	ledgerActions := remediate.Available(in)
	resp := make([]AuditActionResponse, 0, len(ledgerActions))
	for _, a := range ledgerActions {
		resp = append(resp, AuditActionResponse{
			Event:          string(a.Event),
			To:             a.To,
			RequiresReason: a.RequiresNote,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListReviewQueue returns the workpapers waiting on the viewer's rank.
func (h *Handler) ListReviewQueue(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin", "member")
	if !ok {
		return
	}

	rows, err := h.Queries.ListReviewQueueForMember(r.Context(), db.ListReviewQueueForMemberParams{
		WorkspaceID: wsUUID,
		MemberID:    member.ID,
		Limit:       reviewQueueLimit,
	})
	if err != nil {
		slog.Warn("ListReviewQueue failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to read the review queue")
		return
	}

	prefix := h.getIssuePrefix(r.Context(), wsUUID)
	resp := make([]ReviewQueueItemResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, ReviewQueueItemResponse{
			Issue: issueToResponse(db.Issue{
				ID: row.ID, WorkspaceID: row.WorkspaceID, Number: row.Number,
				Title: row.Title, Description: row.Description, Status: row.Status,
				Priority: row.Priority, AssigneeType: row.AssigneeType, AssigneeID: row.AssigneeID,
				CreatorType: row.CreatorType, CreatorID: row.CreatorID,
				ParentIssueID: row.ParentIssueID, ProjectID: row.ProjectID,
				AcceptanceCriteria: row.AcceptanceCriteria, ContextRefs: row.ContextRefs,
				Position: row.Position, StartDate: row.StartDate, DueDate: row.DueDate,
				Stage: row.Stage, Metadata: row.Metadata, Properties: row.Properties,
				Revision: row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			}, prefix),
			Level:        row.ReviewerLevel,
			WaitingSince: timestampToString(row.UpdatedAt),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// reviewQueueLimit bounds one page. A queue longer than this is a staffing
// problem the interface should not paper over by scrolling forever.
const reviewQueueLimit = 200
