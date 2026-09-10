package handler

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Wiring, permissions and named regressions for the review gate. The transition
// matrix itself is the canonical property of internal/auditgate and is
// exercised as a table there; this file does not replay it.

type auditFixture struct {
	workspaceID string
	projectID   string
	// reviewers holds one member id per level, none of whom is the preparer.
	reviewers map[auditgate.Level]string
	// reviewerUsers maps a level to the user id that authenticates as it.
	reviewerUsers map[auditgate.Level]string
}

// newAuditFixture builds an auditee with an engagement, three seated reviewers,
// and the shared test user as an ordinary member who will act as preparer.
func newAuditFixture(t *testing.T) auditFixture {
	t.Helper()
	wsID := newAuditWorkspace(t)
	enableAuditMode(t, wsID, nil).Want(http.StatusOK)

	projectID := dbfx.Insert(t, "project", testutil.Cols{
		"workspace_id": wsID,
		"title":        "Engagement " + uuid.NewString()[:8],
	})
	dbfx.Cleanup(t, `DELETE FROM audit_role WHERE project_id = $1`, projectID)

	f := auditFixture{
		workspaceID:   wsID,
		projectID:     projectID,
		reviewers:     map[auditgate.Level]string{},
		reviewerUsers: map[auditgate.Level]string{},
	}
	for _, level := range auditgate.Levels() {
		userID := dbfx.User(t, "Reviewer "+string(level), fmt.Sprintf("rev-%s@multica.ai", uuid.NewString()[:8]))
		memberID := dbfx.Member(t, wsID, userID, "member")
		dbfx.Insert(t, "audit_role", testutil.Cols{
			"workspace_id": wsID,
			"project_id":   projectID,
			"member_id":    memberID,
			"level":        string(level),
		})
		f.reviewers[level] = memberID
		f.reviewerUsers[level] = userID
	}
	return f
}

// workpaper creates an issue inside the engagement at the given status.
func (f auditFixture) workpaper(t *testing.T, status string) string {
	t.Helper()
	return dbfx.Issue(t, "Workpaper "+uuid.NewString()[:8], testutil.Cols{
		"workspace_id": f.workspaceID,
		"project_id":   f.projectID,
		"status":       status,
	})
}

// setStatus drives the single-issue update endpoint as the given user.
func (f auditFixture) setStatus(t *testing.T, issueID, status, asUserID string) *testutil.Response {
	t.Helper()
	req := auditRequest(http.MethodPatch, "/api/issues/"+issueID, f.workspaceID, map[string]any{"status": status})
	req.Header.Set("X-User-ID", asUserID)
	return testutil.Call(t, testHandler.UpdateIssue, withURLParam(req, "id", issueID))
}

func (f auditFixture) statusOf(t *testing.T, issueID string) string {
	t.Helper()
	var status string
	dbfx.QueryRow(t, `SELECT status FROM issue WHERE id = $1`, issueID).Scan(&status)
	return status
}

func TestReviewGateAdvancesAWorkpaperThroughTheWholeChain(t *testing.T) {
	f := newAuditFixture(t)
	// The longest chain an engagement can run, walked end to end. Two levels is
	// the default and files after two reviews; that is its own test.
	f.setDepth(t, 3).Want(http.StatusOK)
	wp := f.workpaper(t, auditmode.StatusDrafting)

	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusReviewL2, f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusReviewL3, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusFiled, f.reviewerUsers[auditgate.LevelL3]).Want(http.StatusOK)

	if got := f.statusOf(t, wp); got != auditmode.StatusFiled {
		t.Fatalf("status = %q, want %q", got, auditmode.StatusFiled)
	}
}

func TestSubmittingForReviewRecordsThePreparerOnTheWorkpaper(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusDrafting)
	dbfx.Cleanup(t, `DELETE FROM audit_workpaper WHERE issue_id = $1`, wp)

	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)

	var preparer string
	dbfx.QueryRow(t, `SELECT preparer_id::text FROM audit_workpaper WHERE issue_id = $1`, wp).Scan(&preparer)
	var expected string
	dbfx.QueryRow(t, `SELECT id::text FROM member WHERE workspace_id = $1 AND user_id = $2`, f.workspaceID, testUserID).Scan(&expected)
	if preparer != expected {
		t.Errorf("preparer = %q, want the submitting member %q", preparer, expected)
	}
}

func TestAMemberWithNoReviewerRoleCannotAdvanceAWorkpaper(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusReviewL1)

	f.setStatus(t, wp, auditmode.StatusReviewL2, testUserID).Want(http.StatusForbidden)

	if got := f.statusOf(t, wp); got != auditmode.StatusReviewL1 {
		t.Errorf("status = %q, want the refused write to have changed nothing", got)
	}
}

func TestAFiledWorkpaperRefusesEvenATitleEdit(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusFiled)

	req := auditRequest(http.MethodPatch, "/api/issues/"+wp, f.workspaceID, map[string]any{"title": "edited after filing"})
	testutil.Call(t, testHandler.UpdateIssue, withURLParam(req, "id", wp)).Want(http.StatusConflict)

	var title string
	dbfx.QueryRow(t, `SELECT title FROM issue WHERE id = $1`, wp).Scan(&title)
	if title == "edited after filing" {
		t.Error("a filed workpaper was edited; a correction must be a new version")
	}
}

// THE bypass this control most likely loses to. The batch endpoint takes one
// status for many issues, so if it were ungated a single request could file an
// entire engagement. Both endpoints funnel through the same two write helpers
// precisely so this cannot drift.
func TestBatchUpdateIsGatedJustLikeTheSingleEndpoint(t *testing.T) {
	f := newAuditFixture(t)
	first := f.workpaper(t, auditmode.StatusDrafting)
	second := f.workpaper(t, auditmode.StatusDrafting)

	req := auditRequest(http.MethodPatch, "/api/issues/batch", f.workspaceID, map[string]any{
		"issue_ids": []string{first, second},
		"updates":   map[string]any{"status": auditmode.StatusFiled},
	})
	testutil.Call(t, testHandler.BatchUpdateIssues, req).WantOneOf(http.StatusForbidden, http.StatusConflict)

	for _, id := range []string{first, second} {
		if got := f.statusOf(t, id); got != auditmode.StatusDrafting {
			t.Errorf("issue %s status = %q; the batch must not have filed it", id, got)
		}
	}
}

// A batch is decided in full before the first write. Without that, an allowed
// item would commit and a later refusal would still return an error — leaving
// the caller's view of the resulting state simply wrong.
func TestABatchWithOneIllegalTransitionAppliesNothing(t *testing.T) {
	f := newAuditFixture(t)
	legal := f.workpaper(t, auditmode.StatusReviewL1)
	illegal := f.workpaper(t, auditmode.StatusDrafting)

	req := auditRequest(http.MethodPatch, "/api/issues/batch", f.workspaceID, map[string]any{
		"issue_ids": []string{legal, illegal},
		"updates":   map[string]any{"status": auditmode.StatusReviewL2},
	})
	req.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL1])
	testutil.Call(t, testHandler.BatchUpdateIssues, req).WantOneOf(http.StatusForbidden, http.StatusConflict)

	if got := f.statusOf(t, legal); got != auditmode.StatusReviewL1 {
		t.Errorf("the legal item moved to %q; a refused batch must apply nothing", got)
	}
	if got := f.statusOf(t, illegal); got != auditmode.StatusDrafting {
		t.Errorf("the illegal item moved to %q", got)
	}
}

// Custom-to-built-in is the escape the old short circuit left open: the guard
// skipped its transaction whenever the TARGET was a built-in key.
// The two-request bypass: engagement membership is what makes an issue a
// workpaper, so dropping the project mid-review would take the workpaper out of
// the gate's reach and the next write could file it unreviewed.
func TestAWorkpaperCannotBeMovedOutOfItsEngagementWhileInReview(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusReviewL2)

	req := auditRequest(http.MethodPatch, "/api/issues/"+wp, f.workspaceID, map[string]any{"project_id": nil})
	testutil.Call(t, testHandler.UpdateIssue, withURLParam(req, "id", wp)).Want(http.StatusConflict)

	var projectID *string
	dbfx.QueryRow(t, `SELECT project_id::text FROM issue WHERE id = $1`, wp).Scan(&projectID)
	if projectID == nil {
		t.Fatal("the workpaper left its engagement; the next status write would not be governed at all")
	}
}

// The mirror image, which would defeat the create-time guard.
func TestAnIssueCarryingAReviewStatusCannotBeMovedIntoAnEngagement(t *testing.T) {
	f := newAuditFixture(t)
	// Created outside any engagement, so the create gate does not see it.
	loose := dbfx.Issue(t, "Loose "+uuid.NewString()[:8], testutil.Cols{
		"workspace_id": f.workspaceID,
		"status":       auditmode.StatusFiled,
	})

	req := auditRequest(http.MethodPatch, "/api/issues/"+loose, f.workspaceID, map[string]any{"project_id": f.projectID})
	testutil.Call(t, testHandler.UpdateIssue, withURLParam(req, "id", loose)).
		WantOneOf(http.StatusForbidden, http.StatusConflict)

	if n := dbfx.Count(t, `SELECT COUNT(*) FROM issue WHERE id = $1 AND project_id = $2`, loose, f.projectID); n != 0 {
		t.Error("an issue arrived in the engagement already filed, as a workpaper no reviewer had seen")
	}
}

// A repeated id used to be decided once against the pre-state, then written
// twice: the second pass read the status the first had just set and refused it,
// aborting AFTER the first write had committed — exactly the partial-apply the
// preflight exists to prevent.
func TestARepeatedIssueIdInABatchIsNotAPartialApply(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusReviewL1)

	req := auditRequest(http.MethodPatch, "/api/issues/batch", f.workspaceID, map[string]any{
		"issue_ids": []string{wp, wp},
		"updates":   map[string]any{"status": auditmode.StatusReviewL2},
	})
	req.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL1])
	testutil.Call(t, testHandler.BatchUpdateIssues, req).Want(http.StatusOK)

	if got := f.statusOf(t, wp); got != auditmode.StatusReviewL2 {
		t.Errorf("status = %q, want %q: the duplicate must be a no-op, not a failure", got, auditmode.StatusReviewL2)
	}
}

func TestAWorkpaperCannotBeMarkedDoneToLeaveTheChain(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusReviewL2)

	f.setStatus(t, wp, "done", f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusConflict)

	if got := f.statusOf(t, wp); got != auditmode.StatusReviewL2 {
		t.Errorf("status = %q, want the workpaper still in review", got)
	}
}

func TestAnIssueCannotBeCreatedDirectlyIntoTheChain(t *testing.T) {
	f := newAuditFixture(t)

	req := auditRequest(http.MethodPost, "/api/issues", f.workspaceID, map[string]any{
		"title":      "fabricated",
		"project_id": f.projectID,
		"status":     auditmode.StatusFiled,
	})
	testutil.Call(t, testHandler.CreateIssue, req).WantOneOf(http.StatusForbidden, http.StatusConflict)

	if n := dbfx.Count(t, `SELECT COUNT(*) FROM issue WHERE project_id = $1 AND status = $2`, f.projectID, auditmode.StatusFiled); n != 0 {
		t.Error("an issue was created directly at filed")
	}
}

// A remediation item is an issue that belongs to no engagement. It has to keep
// moving through ordinary statuses, or enabling audit mode would freeze every
// non-workpaper issue in the workspace.
func TestIssuesOutsideAnEngagementAreUngoverned(t *testing.T) {
	f := newAuditFixture(t)
	remediation := dbfx.Issue(t, "Remediation "+uuid.NewString()[:8], testutil.Cols{
		"workspace_id": f.workspaceID,
		"status":       "todo",
	})

	f.setStatus(t, remediation, "done", testUserID).Want(http.StatusOK)
}

// The gate must be inert where audit mode was never enabled, including for a
// workspace that happens to have hand-made statuses with the same keys.
func TestAWorkspaceThatIsNotAnAuditeeIsUnaffected(t *testing.T) {
	wsID := newAuditWorkspace(t)
	projectID := dbfx.Insert(t, "project", testutil.Cols{
		"workspace_id": wsID,
		"title":        "Ordinary project",
	})
	// Same key, no audit mode: the key check alone would wrongly engage here.
	dbfx.Insert(t, "issue_status", testutil.Cols{
		"workspace_id": wsID,
		"key":          auditmode.StatusReviewL1,
		"name":         "Hand-made Review",
		"category":     issuestatus.InReview,
		"color":        "#123456",
	})
	issueID := dbfx.Issue(t, "Ordinary issue", testutil.Cols{
		"workspace_id": wsID,
		"project_id":   projectID,
		"status":       auditmode.StatusReviewL1,
	})

	req := auditRequest(http.MethodPatch, "/api/issues/"+issueID, wsID, map[string]any{"status": "done"})
	testutil.Call(t, testHandler.UpdateIssue, withURLParam(req, "id", issueID)).Want(http.StatusOK)
}

func TestSeatingAReviewerIsOwnerAdminOnlyAndNeverAnAgent(t *testing.T) {
	f := newAuditFixture(t)
	userID := dbfx.User(t, "Plain", fmt.Sprintf("plain-%s@multica.ai", uuid.NewString()[:8]))
	memberID := dbfx.Member(t, f.workspaceID, userID, "member")

	t.Run("plain member", func(t *testing.T) {
		req := auditRequest(http.MethodPut, "/api/projects/"+f.projectID+"/audit-roles", f.workspaceID,
			map[string]any{"member_id": memberID, "level": string(auditgate.LevelL1)})
		req.Header.Set("X-User-ID", userID)
		testutil.Call(t, testHandler.SetAuditRole, withURLParam(req, "id", f.projectID)).Want(http.StatusForbidden)
	})

	t.Run("agent actor", func(t *testing.T) {
		req := auditRequest(http.MethodPut, "/api/projects/"+f.projectID+"/audit-roles", f.workspaceID,
			map[string]any{"member_id": memberID, "level": string(auditgate.LevelL1)})
		req.Header.Set("X-Actor-Source", "task_token")
		req.Header.Set("X-Agent-ID", uuid.NewString())
		testutil.Call(t, testHandler.SetAuditRole, withURLParam(req, "id", f.projectID)).Want(http.StatusForbidden)
	})
}

// One level per person per engagement is what makes "nobody reviews at two
// levels" structural. Re-seating replaces rather than accumulating. The second
// rank is L2 because the fixture's engagement runs two levels; seating a rank
// the engagement does not run is refused, and is its own test.
func TestSeatingAReviewerAtASecondLevelReplacesTheFirst(t *testing.T) {
	f := newAuditFixture(t)
	member := f.reviewers[auditgate.LevelL1]

	req := auditRequest(http.MethodPut, "/api/projects/"+f.projectID+"/audit-roles", f.workspaceID,
		map[string]any{"member_id": member, "level": string(auditgate.LevelL2)})
	testutil.Call(t, testHandler.SetAuditRole, withURLParam(req, "id", f.projectID)).Want(http.StatusOK)

	if n := dbfx.Count(t, `SELECT COUNT(*) FROM audit_role WHERE project_id = $1 AND member_id = $2`, f.projectID, member); n != 1 {
		t.Errorf("audit_role rows = %d, want 1: a person holds at most one level per engagement", n)
	}
	var level string
	dbfx.QueryRow(t, `SELECT level FROM audit_role WHERE project_id = $1 AND member_id = $2`, f.projectID, member).Scan(&level)
	if level != string(auditgate.LevelL2) {
		t.Errorf("level = %q, want %q", level, auditgate.LevelL2)
	}
}

func TestAReviewerFromAnotherWorkspaceCannotBeSeated(t *testing.T) {
	f := newAuditFixture(t)
	otherWS := newAuditWorkspace(t)
	outsiderUser := dbfx.User(t, "Outsider", fmt.Sprintf("out-%s@multica.ai", uuid.NewString()[:8]))
	outsider := dbfx.Member(t, otherWS, outsiderUser, "member")

	req := auditRequest(http.MethodPut, "/api/projects/"+f.projectID+"/audit-roles", f.workspaceID,
		map[string]any{"member_id": outsider, "level": string(auditgate.LevelL1)})
	testutil.Call(t, testHandler.SetAuditRole, withURLParam(req, "id", f.projectID)).Want(http.StatusBadRequest)
}

func TestDeletingAnEngagementTakesItsReviewerRolesWithIt(t *testing.T) {
	f := newAuditFixture(t)
	if n := dbfx.Count(t, `SELECT COUNT(*) FROM audit_role WHERE project_id = $1`, f.projectID); n != 3 {
		t.Fatalf("audit_role rows = %d, want 3 before delete", n)
	}

	req := auditRequest(http.MethodDelete, "/api/projects/"+f.projectID, f.workspaceID, nil)
	testutil.Call(t, testHandler.DeleteProject, withURLParam(req, "id", f.projectID)).
		WantOneOf(http.StatusOK, http.StatusNoContent)

	if n := dbfx.Count(t, `SELECT COUNT(*) FROM audit_role WHERE project_id = $1`, f.projectID); n != 0 {
		t.Errorf("audit_role rows = %d after deleting the engagement, want 0: there are no cascades in this schema", n)
	}
}

func TestDeletingAWorkpaperTakesItsPreparerRecordWithIt(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusDrafting)
	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)
	if n := dbfx.Count(t, `SELECT COUNT(*) FROM audit_workpaper WHERE issue_id = $1`, wp); n != 1 {
		t.Fatalf("audit_workpaper rows = %d, want 1 before delete", n)
	}

	if err := testHandler.Queries.DeleteIssue(context.Background(), db.DeleteIssueParams{
		ID:          parseUUID(wp),
		WorkspaceID: parseUUID(f.workspaceID),
	}); err != nil {
		t.Fatalf("delete issue: %v", err)
	}
	if n := dbfx.Count(t, `SELECT COUNT(*) FROM audit_workpaper WHERE issue_id = $1`, wp); n != 0 {
		t.Errorf("audit_workpaper rows = %d after deleting the issue, want 0", n)
	}
}

// Two levels of review mean two PEOPLE looked at it.
//
// The gate checked the rank the actor holds NOW and whether they prepared the
// workpaper — never who signed the level below. A reviewer's rank changes
// mid-flight for ordinary reasons (a promotion, a transfer, someone covering an
// absence), so one person could pass at 一级复核, be re-seated as 项目经理, and
// sign the same workpaper again. The configured depth was then just a shape.
func TestOnePersonCannotSignTwoLevelsOfTheSameWorkpaper(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusDrafting)
	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)

	// 主审 passes it on.
	l1User := f.reviewerUsers[auditgate.LevelL1]
	f.setStatus(t, wp, auditmode.StatusReviewL2, l1User).Want(http.StatusOK)

	// The same person is re-seated one level up — a replacement, not a second
	// rank: audit_role is unique per (engagement, member).
	req := auditRequest(http.MethodPut, "/api/projects/"+f.projectID+"/audit-roles", f.workspaceID,
		map[string]any{"member_id": f.reviewers[auditgate.LevelL1], "level": string(auditgate.LevelL2)})
	testutil.Call(t, testHandler.SetAuditRole, withURLParam(req, "id", f.projectID)).Want(http.StatusOK)

	// ...and tries to sign the level they just handed the workpaper to.
	body := f.setStatus(t, wp, auditmode.StatusFiled, l1User).Want(http.StatusForbidden).Map()
	if code, _ := body["code"].(string); code != "same_reviewer" {
		t.Errorf("refused as %q, want same_reviewer — the reader has to learn that the SECOND signature is the problem, not their rank", code)
	}
	if got := f.statusOf(t, wp); got != auditmode.StatusReviewL2 {
		t.Errorf("status = %q, want the refused signature to have changed nothing", got)
	}

	// Someone else at that rank can still file it, so the refusal is about the
	// person and not about the step.
	other := dbfx.User(t, "另一位项目经理", "mgr-"+uuid.NewString()[:8]+"@multica.ai")
	otherMember := dbfx.Member(t, f.workspaceID, other, "member")
	seat := auditRequest(http.MethodPut, "/api/projects/"+f.projectID+"/audit-roles", f.workspaceID,
		map[string]any{"member_id": otherMember, "level": string(auditgate.LevelL2)})
	testutil.Call(t, testHandler.SetAuditRole, withURLParam(seat, "id", f.projectID)).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusFiled, other).Want(http.StatusOK)
}

// The other half: signing the SAME level twice is what a rejection loop IS, and
// must keep working. A rule that stopped it would make one rejection retire the
// reviewer who caught the problem.
func TestTheSameReviewerMaySignTheSameLevelAgainAfterARejection(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusDrafting)
	l1User := f.reviewerUsers[auditgate.LevelL1]

	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusReviewL2, l1User).Want(http.StatusOK)

	// 项目经理 sends it back to the preparer, who resubmits.
	reject := auditRequest(http.MethodPatch, "/api/issues/"+wp, f.workspaceID, map[string]any{
		"status":      auditmode.StatusDrafting,
		"review_note": "抽样依据没写清楚",
	})
	reject.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL2])
	testutil.Call(t, testHandler.UpdateIssue, withURLParam(reject, "id", wp)).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)

	// The same 主审 signs their own level a second time.
	f.setStatus(t, wp, auditmode.StatusReviewL2, l1User).Want(http.StatusOK)
}
