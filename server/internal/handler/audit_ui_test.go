package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// The two reads the interface makes. The matrix behind the actions is the
// canonical property of internal/auditgate and is not replayed here; what needs
// a database is the queue's join and the endpoints' permissions.

func (f auditFixture) actionsFor(t *testing.T, issueID, asUserID string) []AuditActionResponse {
	t.Helper()
	req := auditRequest(http.MethodGet, "/api/issues/"+issueID+"/audit-actions", f.workspaceID, nil)
	req.Header.Set("X-User-ID", asUserID)
	var out []AuditActionResponse
	testutil.Call(t, testHandler.ListAuditActions, withURLParam(req, "id", issueID)).
		Want(http.StatusOK).JSON(&out)
	return out
}

func (f auditFixture) queueFor(t *testing.T, asUserID string) []ReviewQueueItemResponse {
	t.Helper()
	req := auditRequest(http.MethodGet, "/api/audit/review-queue", f.workspaceID, nil)
	req.Header.Set("X-User-ID", asUserID)
	var out []ReviewQueueItemResponse
	testutil.Call(t, testHandler.ListReviewQueue, req).Want(http.StatusOK).JSON(&out)
	return out
}

func TestTheActionsOfferedMatchTheRankHeld(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusReviewL1)

	offered := map[string]bool{}
	for _, a := range f.actionsFor(t, wp, f.reviewerUsers[auditgate.LevelL1]) {
		offered[a.To] = true
	}
	if !offered[auditmode.StatusReviewL2] {
		t.Error("the first level was not offered the pass")
	}
	if !offered[auditmode.StatusDrafting] {
		t.Error("the first level was not offered the return")
	}

	// A different level is offered no step in the chain here — it may still
	// cancel, which any reviewer may do at any stage, but it must not be able
	// to advance or return a workpaper sitting at someone else's level.
	for _, a := range f.actionsFor(t, wp, f.reviewerUsers[auditgate.LevelL2]) {
		if a.To != "cancelled" {
			t.Errorf("the second level was offered %q on a first-level workpaper", a.To)
		}
	}
}

// The absence of buttons on a filed workpaper is the rule showing through the
// interface rather than a bug the reader has to guess at.
func TestAFiledWorkpaperOffersNothing(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusFiled)
	for _, level := range auditgate.Levels() {
		if actions := f.actionsFor(t, wp, f.reviewerUsers[level]); len(actions) != 0 {
			t.Errorf("%s was offered %d actions on a filed workpaper", level, len(actions))
		}
	}
}

func TestAnOrdinaryIssueOffersNoAuditActions(t *testing.T) {
	f := newAuditFixture(t)
	remediation := dbfx.Issue(t, "Remediation", testutil.Cols{
		"workspace_id": f.workspaceID,
		"status":       "todo",
	})
	if actions := f.actionsFor(t, remediation, testUserID); len(actions) != 0 {
		t.Errorf("an issue outside an engagement offered %d audit actions", len(actions))
	}
}

// A returned workpaper needs a reason, and the interface has to say so BEFORE
// the reviewer presses anything — otherwise the requirement is discovered as a
// refusal.
func TestTheReturnActionAsksForAReasonAndThePassDoesNot(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusReviewL2)
	for _, a := range f.actionsFor(t, wp, f.reviewerUsers[auditgate.LevelL2]) {
		want := a.To == auditmode.StatusDrafting
		if a.RequiresReason != want {
			t.Errorf("action to %q: requires_reason = %v, want %v", a.To, a.RequiresReason, want)
		}
	}
}

// THE case the queue exists for. Filtering engagements and statuses
// independently — all the ordinary issue list can do — returns the cross
// product, showing a reviewer work that belongs to a level they do not hold.
func TestTheQueuePairsEachEngagementWithTheRankHeldThere(t *testing.T) {
	f := newAuditFixture(t)
	// A second engagement where the same person holds a different rank.
	otherProject := dbfx.Insert(t, "project", testutil.Cols{
		"workspace_id": f.workspaceID,
		"title":        "Second engagement",
	})
	dbfx.Cleanup(t, `DELETE FROM audit_role WHERE project_id = $1`, otherProject)
	dbfx.Insert(t, "audit_role", testutil.Cols{
		"workspace_id": f.workspaceID,
		"project_id":   otherProject,
		"member_id":    f.reviewers[auditgate.LevelL1],
		"level":        string(auditgate.LevelL2),
	})

	mine := f.workpaper(t, auditmode.StatusReviewL1) // first engagement, l1 — mine
	theirs := dbfx.Issue(t, "Not mine", testutil.Cols{
		"workspace_id": f.workspaceID,
		"project_id":   otherProject,
		"status":       auditmode.StatusReviewL1, // second engagement, l1 — NOT mine, I am l2 there
	})
	alsoMine := dbfx.Issue(t, "Also mine", testutil.Cols{
		"workspace_id": f.workspaceID,
		"project_id":   otherProject,
		"status":       auditmode.StatusReviewL2,
	})

	got := map[string]bool{}
	for _, item := range f.queueFor(t, f.reviewerUsers[auditgate.LevelL1]) {
		got[item.Issue.ID] = true
	}
	if !got[mine] || !got[alsoMine] {
		t.Errorf("queue missed a workpaper at the rank held: %v", got)
	}
	if got[theirs] {
		t.Error("queue included another level's workpaper — this is the cross product the join exists to avoid")
	}
}

// A workpaper nobody may act on has no business sitting in a queue.
func TestTheQueueExcludesTheViewersOwnWorkpapers(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusDrafting)

	// The first-level reviewer prepares it themselves, then submits.
	req := auditRequest(http.MethodPatch, "/api/issues/"+wp, f.workspaceID,
		map[string]any{"status": auditmode.StatusReviewL1})
	req.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL1])
	testutil.Call(t, testHandler.UpdateIssue, withURLParam(req, "id", wp)).Want(http.StatusOK)

	for _, item := range f.queueFor(t, f.reviewerUsers[auditgate.LevelL1]) {
		if item.Issue.ID == wp {
			t.Error("a reviewer's own workpaper appeared in their queue; they can never act on it")
		}
	}
}

func TestAMemberWithNoRankHasAnEmptyQueue(t *testing.T) {
	f := newAuditFixture(t)
	f.workpaper(t, auditmode.StatusReviewL1)
	if items := f.queueFor(t, testUserID); len(items) != 0 {
		t.Errorf("a member holding no rank saw %d workpapers", len(items))
	}
}

func TestTheQueueIsEmptyInAWorkspaceThatIsNotAnAuditee(t *testing.T) {
	wsID := newAuditWorkspace(t)
	userID := dbfx.User(t, "Plain", fmt.Sprintf("plain-%s@multica.ai", uuid.NewString()[:8]))
	dbfx.Member(t, wsID, userID, "member")

	req := auditRequest(http.MethodGet, "/api/audit/review-queue", wsID, nil)
	req.Header.Set("X-User-ID", userID)
	var out []ReviewQueueItemResponse
	testutil.Call(t, testHandler.ListReviewQueue, req).Want(http.StatusOK).JSON(&out)
	if len(out) != 0 {
		t.Errorf("a workspace that is not an auditee returned %d queue items", len(out))
	}
}

// A refusal has to carry a code, or the client has nothing to translate and
// falls back to showing the server's English sentence.
func TestARefusalCarriesAMachineReadableCode(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusReviewL1)

	body := f.setStatus(t, wp, auditmode.StatusReviewL2, testUserID).
		Want(http.StatusForbidden).Map()
	if body["code"] != string(auditgate.DenyLevelRequired) {
		t.Errorf("code = %v, want %q", body["code"], auditgate.DenyLevelRequired)
	}
	if body["error"] == "" || body["error"] == nil {
		t.Error("the sentence is gone; it is the fallback for anything not yet translated")
	}
}
