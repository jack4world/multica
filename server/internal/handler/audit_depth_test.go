package handler

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/auditmeta"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// An engagement's review depth. The matrix is the canonical property of
// internal/auditgate and runs at every depth there; what needs a database is
// the default, the two rules that are not about the matrix, and that a
// two-level engagement actually files after two reviews.

func (f auditFixture) setDepth(t *testing.T, levels int) *testutil.Response {
	t.Helper()
	req := auditRequest(http.MethodPut, "/api/projects/"+f.projectID, f.workspaceID,
		map[string]any{"review_levels": levels})
	return testutil.Call(t, testHandler.UpdateProject, withURLParam(req, "id", f.projectID))
}

// The whole point of this piece, and one character away from being three.
func TestAnEngagementDefaultsToTwoReviewLevels(t *testing.T) {
	f := newAuditFixture(t)
	var levels int
	dbfx.QueryRow(t, `SELECT review_levels FROM project WHERE id = $1`, f.projectID).Scan(&levels)
	if levels != 2 {
		t.Errorf("review_levels = %d, want 2 — a company's internal audit function runs two, "+
			"and forcing a third means inventing a reviewer", levels)
	}
}

// At two levels, 项目经理 files. Nobody has to be invented.
func TestATwoLevelEngagementFilesAfterTwoReviews(t *testing.T) {
	f := newAuditFixture(t)
	f.setDepth(t, 2).Want(http.StatusOK)
	wp := f.workpaper(t, auditmode.StatusDrafting)

	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusReviewL2, f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusFiled, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	if got := f.statusOf(t, wp); got != auditmode.StatusFiled {
		t.Fatalf("status = %q, want the second level to have filed it", got)
	}
}

func TestATwoLevelEngagementCannotReachTheThirdLevel(t *testing.T) {
	f := newAuditFixture(t)
	f.setDepth(t, 2).Want(http.StatusOK)
	wp := f.workpaper(t, auditmode.StatusReviewL2)

	body := f.setStatus(t, wp, auditmode.StatusReviewL3, f.reviewerUsers[auditgate.LevelL2]).
		Want(http.StatusConflict).Map()
	// Named, not reported as a generic illegal transition: the reader needs to
	// know this engagement has fewer levels, not that they picked a step out of
	// order.
	if msg, _ := body["error"].(string); msg == "" {
		t.Error("the refusal carries no message")
	}
}

// A 三级复核人 on a two-level engagement is a configuration error, and catching
// it when it is made is far cheaper than catching it when a workpaper cannot
// move and nobody knows why.
func TestARankTheEngagementDoesNotRunCannotBeSeated(t *testing.T) {
	f := newAuditFixture(t)
	f.setDepth(t, 2).Want(http.StatusOK)
	userID := dbfx.User(t, "Extra", "extra-"+uuid.NewString()[:8]+"@multica.ai")
	memberID := dbfx.Member(t, f.workspaceID, userID, "member")

	req := auditRequest(http.MethodPut, "/api/projects/"+f.projectID+"/audit-roles", f.workspaceID,
		map[string]any{"member_id": memberID, "level": string(auditgate.LevelL3)})
	testutil.Call(t, testHandler.SetAuditRole, withURLParam(req, "id", f.projectID)).
		Want(http.StatusBadRequest)
}

// Lowering the depth under a workpaper already at a deeper stage would strand
// it: no transition would reach it and none would leave. A configuration
// correction must not become a data problem.
func TestLoweringTheDepthUnderAWorkpaperIsRefused(t *testing.T) {
	f := newAuditFixture(t)
	f.setDepth(t, 3).Want(http.StatusOK)
	wp := f.workpaper(t, auditmode.StatusReviewL3)

	f.setDepth(t, 2).Want(http.StatusConflict)

	var levels int
	dbfx.QueryRow(t, `SELECT review_levels FROM project WHERE id = $1`, f.projectID).Scan(&levels)
	if levels != 3 {
		t.Errorf("review_levels = %d, want the refused change to have altered nothing", levels)
	}
	if got := f.statusOf(t, wp); got != auditmode.StatusReviewL3 {
		t.Errorf("workpaper status = %q, want untouched", got)
	}
}

func TestLoweringTheDepthIsAllowedWhenNothingIsStranded(t *testing.T) {
	f := newAuditFixture(t)
	f.setDepth(t, 3).Want(http.StatusOK)
	f.workpaper(t, auditmode.StatusReviewL1)

	f.setDepth(t, 1).Want(http.StatusOK)
}

func TestAnEngagementRecordsItsPhase(t *testing.T) {
	f := newAuditFixture(t)
	req := auditRequest(http.MethodPut, "/api/projects/"+f.projectID, f.workspaceID,
		map[string]any{"audit_phase": auditmeta.PhaseFieldwork})
	var out ProjectResponse
	testutil.Call(t, testHandler.UpdateProject, withURLParam(req, "id", f.projectID)).
		Want(http.StatusOK).JSON(&out)
	if out.AuditPhase == nil || *out.AuditPhase != auditmeta.PhaseFieldwork {
		t.Errorf("audit_phase = %v", out.AuditPhase)
	}
}

func TestAnUnknownPhaseIsRefused(t *testing.T) {
	f := newAuditFixture(t)
	req := auditRequest(http.MethodPut, "/api/projects/"+f.projectID, f.workspaceID,
		map[string]any{"audit_phase": "现场"})
	testutil.Call(t, testHandler.UpdateProject, withURLParam(req, "id", f.projectID)).
		Want(http.StatusBadRequest)
}
