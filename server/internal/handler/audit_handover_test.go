package handler

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// The handover's effect, through the gate. The transition matrix itself is the
// canonical property of internal/auditgate; what needs a database is that the
// agent's account survives, and that a person cannot perform one.

func (f auditFixture) handover(t *testing.T, issueID, agentID, note string) *testutil.Response {
	t.Helper()
	body := map[string]any{"status": auditmode.StatusAgentDelivered}
	if note != "" {
		body["review_note"] = note
	}
	req := auditRequest(http.MethodPatch, "/api/issues/"+issueID, f.workspaceID, body)
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", agentID)
	return testutil.Call(t, testHandler.UpdateIssue, withURLParam(req, "id", issueID))
}

func TestAnAgentHandsOverADraftAndItsAccountSurvives(t *testing.T) {
	f := newAuditFixture(t)
	agentID := createHandlerTestAgent(t, "Audit agent "+uuid.NewString()[:6], nil)
	wp := dbfx.Issue(t, "Sampling workpaper", testutil.Cols{
		"workspace_id": f.workspaceID,
		"project_id":   f.projectID,
		"status":       auditmode.StatusDrafting,
	})
	dbfx.Cleanup(t, `DELETE FROM activity_log WHERE issue_id = $1`, wp)

	const note = "Sampled 40 of 612 payments. Could NOT verify two 2024-11 vouchers — scans unreadable."
	f.handover(t, wp, agentID, note).Want(http.StatusOK)

	if got := f.statusOf(t, wp); got != auditmode.StatusAgentDelivered {
		t.Fatalf("status = %q, want %q", got, auditmode.StatusAgentDelivered)
	}

	entries := f.trailFor(t, wp)
	last := entries[len(entries)-1]
	if last.Action != string(auditgate.EventHandedOver) {
		t.Errorf("action = %q, want the handover recorded distinctly from a submission", last.Action)
	}
	if last.Details["reason"] != note {
		t.Errorf("note = %v, want the agent's account preserved verbatim", last.Details["reason"])
	}
	if last.ActorType != "agent" {
		t.Errorf("actor_type = %q, want agent — the trail has to show where the draft came from", last.ActorType)
	}
}

// The agent's account has to survive the human's edits, or a filed workpaper
// carries the human's version of what the agent said.
func TestTheHandoverNoteSurvivesTheDraftBeingAdopted(t *testing.T) {
	f := newAuditFixture(t)
	agentID := createHandlerTestAgent(t, "Audit agent "+uuid.NewString()[:6], nil)
	wp := dbfx.Issue(t, "Workpaper", testutil.Cols{
		"workspace_id": f.workspaceID,
		"project_id":   f.projectID,
		"status":       auditmode.StatusDrafting,
	})
	dbfx.Cleanup(t, `DELETE FROM activity_log WHERE issue_id = $1`, wp)

	const note = "Could not reconcile the Q3 accrual."
	f.handover(t, wp, agentID, note).Want(http.StatusOK)
	// A person adopts it, then edits.
	f.setStatus(t, wp, auditmode.StatusDrafting, testUserID).Want(http.StatusOK)
	edit := auditRequest(http.MethodPatch, "/api/issues/"+wp, f.workspaceID,
		map[string]any{"title": "Workpaper, revised"})
	testutil.Call(t, testHandler.UpdateIssue, withURLParam(edit, "id", wp)).Want(http.StatusOK)

	var found bool
	for _, e := range f.trailFor(t, wp) {
		if e.Action == string(auditgate.EventHandedOver) && e.Details["reason"] == note {
			found = true
		}
	}
	if !found {
		t.Error("the agent's account is gone after the draft was adopted and edited")
	}
}

// "An agent handed this over" has to be a true statement about every such
// trail entry, or the trail cannot be read at all.
func TestAPersonCannotHandOver(t *testing.T) {
	f := newAuditFixture(t)
	wp := dbfx.Issue(t, "Workpaper", testutil.Cols{
		"workspace_id": f.workspaceID,
		"project_id":   f.projectID,
		"status":       auditmode.StatusDrafting,
	})

	req := auditRequest(http.MethodPatch, "/api/issues/"+wp, f.workspaceID,
		map[string]any{"status": auditmode.StatusAgentDelivered, "review_note": "pretending"})
	testutil.Call(t, testHandler.UpdateIssue, withURLParam(req, "id", wp)).
		WantOneOf(http.StatusForbidden, http.StatusConflict)

	if got := f.statusOf(t, wp); got != auditmode.StatusDrafting {
		t.Errorf("status = %q, want the refused write to have changed nothing", got)
	}
}

func TestAnAgentCannotHandOverFromReview(t *testing.T) {
	f := newAuditFixture(t)
	agentID := createHandlerTestAgent(t, "Audit agent "+uuid.NewString()[:6], nil)
	wp := f.workpaper(t, auditmode.StatusReviewL2)

	f.handover(t, wp, agentID, "trying to pull it back").
		WantOneOf(http.StatusForbidden, http.StatusConflict)

	if got := f.statusOf(t, wp); got != auditmode.StatusReviewL2 {
		t.Errorf("status = %q, want an agent unable to pull a workpaper out of review", got)
	}
}
