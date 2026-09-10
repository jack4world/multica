package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/auditreport"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// Wiring, permissions and named regressions for the 审计报告. The chain and the
// rendering are canonical in internal/auditreport and exercised there; what is
// here needs a database: whose signature the engagement's depth requires, what
// the snapshot freezes, and that an issued report stops being writable.

func (f auditFixture) startReport(t *testing.T, asUserID string) *testutil.Response {
	t.Helper()
	req := auditRequest(http.MethodPost, "/api/projects/"+f.projectID+"/reports", f.workspaceID,
		map[string]any{"title": "2025 年度审计报告"})
	req.Header.Set("X-User-ID", asUserID)
	return testutil.Call(t, testHandler.CreateAuditReport, withURLParam(req, "id", f.projectID))
}

func (f auditFixture) writeReport(t *testing.T, reportID string, body map[string]any, asUserID string) *testutil.Response {
	t.Helper()
	req := auditRequest(http.MethodPut, "/api/audit/reports/"+reportID, f.workspaceID, body)
	req.Header.Set("X-User-ID", asUserID)
	return testutil.Call(t, testHandler.UpdateAuditReport, withURLParam(req, "reportId", reportID))
}

// draftReport starts a report and fills it in, as the engagement's 主审.
func (f auditFixture) draftReport(t *testing.T) string {
	t.Helper()
	var out AuditReportResponse
	f.startReport(t, f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusCreated).JSON(&out)
	f.writeReport(t, out.ID, map[string]any{
		"background": "对本单位 2025 年度采购循环进行了检查。",
		"opinion":    "总体有效，两项缺陷。",
	}, f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)
	return out.ID
}

func TestATwoLevelEngagementsReportIsSignedByItsSecondLevel(t *testing.T) {
	f := newAuditFixture(t)
	reportID := f.draftReport(t)

	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusReviewing},
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)

	// The third level is not part of a two-level engagement, and must not be
	// what the report waits for.
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusIssued},
		f.reviewerUsers[auditgate.LevelL3]).Want(http.StatusForbidden)

	var out AuditReportResponse
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusIssued},
		f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK).JSON(&out)
	if out.Status != auditreport.StatusIssued || out.IssuedBy == "" {
		t.Errorf("report = %+v, want issued with a signatory recorded", out)
	}
}

func TestAThreeLevelEngagementsReportWaitsForItsThirdLevel(t *testing.T) {
	f := newAuditFixture(t)
	f.setDepth(t, 3).Want(http.StatusOK)
	reportID := f.draftReport(t)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusReviewing},
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)

	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusIssued},
		f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusForbidden)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusIssued},
		f.reviewerUsers[auditgate.LevelL3]).Want(http.StatusOK)
}

func TestAnIssuedReportRefusesEveryEdit(t *testing.T) {
	f := newAuditFixture(t)
	reportID := f.draftReport(t)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusReviewing},
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusIssued},
		f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	f.writeReport(t, reportID, map[string]any{"opinion": "改一下"},
		f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusConflict)

	var out AuditReportResponse
	req := auditRequest(http.MethodGet, "/api/audit/reports/"+reportID, f.workspaceID, nil)
	testutil.Call(t, testHandler.GetAuditReport, withURLParam(req, "reportId", reportID)).
		Want(http.StatusOK).JSON(&out)
	if out.Opinion == "改一下" {
		t.Error("the refused edit landed on an issued report")
	}
}

// THE reason the snapshot exists: an item closed in March must not rewrite a
// report issued in January, which is the document 后续审计 reads.
func TestAnIssuedReportKeepsWhatItCited(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "采购部")
	issueID := f.item(t, dept)
	reportID := f.draftReport(t)

	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusReviewing},
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)
	var issued AuditReportResponse
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusIssued},
		f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK).JSON(&issued)
	if len(issued.Findings) != 1 || issued.Findings[0].IssueID != issueID {
		t.Fatalf("the issued report cited %+v, want the one item the engagement raised", issued.Findings)
	}
	citedStatus := issued.Findings[0].Status

	// The item moves on afterwards.
	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)

	var reread AuditReportResponse
	req := auditRequest(http.MethodGet, "/api/audit/reports/"+reportID, f.workspaceID, nil)
	testutil.Call(t, testHandler.GetAuditReport, withURLParam(req, "reportId", reportID)).
		Want(http.StatusOK).JSON(&reread)
	if len(reread.Findings) != 1 || reread.Findings[0].Status != citedStatus {
		t.Errorf("the issued report now says %+v; it must still say what it said when it went out", reread.Findings)
	}
}

// A draft is the other half of the same rule: it shows the ledger as it stands,
// because it has not asserted anything yet.
func TestADraftShowsTheLedgerAsItStands(t *testing.T) {
	f := newAuditFixture(t)
	reportID := f.draftReport(t)
	dept := f.department(t, "财务部")
	f.item(t, dept)

	var out AuditReportResponse
	req := auditRequest(http.MethodGet, "/api/audit/reports/"+reportID, f.workspaceID, nil)
	testutil.Call(t, testHandler.GetAuditReport, withURLParam(req, "reportId", reportID)).
		Want(http.StatusOK).JSON(&out)
	if len(out.Findings) != 1 {
		t.Errorf("the draft cites %d items, want the one raised after it was started", len(out.Findings))
	}
}

func TestTheIssuedReportRecordsTheWorkBehindIt(t *testing.T) {
	f := newAuditFixture(t)
	f.workpaper(t, auditmode.StatusFiled)
	f.workpaper(t, auditmode.StatusDrafting)
	reportID := f.draftReport(t)

	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusReviewing},
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)
	var out AuditReportResponse
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusIssued},
		f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK).JSON(&out)

	if out.WorkpaperCount != 2 || out.FiledWorkpapers != 1 {
		t.Errorf("workpapers = %d (%d filed), want 2 and 1: a claim about the work has to be checkable",
			out.WorkpaperCount, out.FiledWorkpapers)
	}
}

func TestASecondUnsignedReportIsRefused(t *testing.T) {
	f := newAuditFixture(t)
	f.draftReport(t)
	f.startReport(t, f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusConflict)
}

func TestTheNextVersionStartsOnceTheLastOneIsIssued(t *testing.T) {
	f := newAuditFixture(t)
	reportID := f.draftReport(t)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusReviewing},
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusIssued},
		f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	var second AuditReportResponse
	f.startReport(t, f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusCreated).JSON(&second)
	if second.Version != 2 {
		t.Errorf("version = %d, want 2: a correction is a new version, not an edit", second.Version)
	}
}

func TestReturningAReportNeedsAReason(t *testing.T) {
	f := newAuditFixture(t)
	reportID := f.draftReport(t)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusReviewing},
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)

	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusDrafting},
		f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusBadRequest)
	f.writeReport(t, reportID, map[string]any{
		"status": auditreport.StatusDrafting,
		"reason": "审计意见与底稿结论不一致",
	}, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)
}

func TestStartingAReportNeedsARankOnTheEngagement(t *testing.T) {
	f := newAuditFixture(t)
	outsiderUser := dbfx.User(t, "Outsider", "rep-"+uuid.NewString()[:8]+"@multica.ai")
	dbfx.Member(t, f.workspaceID, outsiderUser, "member")
	f.startReport(t, outsiderUser).Want(http.StatusForbidden)
}

func TestTheExportCarriesWhoTheReportIsAbout(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "采购部")
	f.item(t, dept)
	reportID := f.draftReport(t)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusReviewing},
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusIssued},
		f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	req := auditRequest(http.MethodGet, "/api/audit/reports/"+reportID+"/export", f.workspaceID, nil)
	body := testutil.Call(t, testHandler.ExportAuditReport, withURLParam(req, "reportId", reportID)).
		Want(http.StatusOK).Text()

	for _, want := range []string{"被审计单位", "审计项目", "签发", "整改事项"} {
		if !strings.Contains(body, want) {
			t.Errorf("the exported report is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "未签发") {
		t.Error("an issued report exported as a draft")
	}
}

func TestADraftExportsMarkedAsADraft(t *testing.T) {
	f := newAuditFixture(t)
	reportID := f.draftReport(t)
	req := auditRequest(http.MethodGet, "/api/audit/reports/"+reportID+"/export", f.workspaceID, nil)
	body := testutil.Call(t, testHandler.ExportAuditReport, withURLParam(req, "reportId", reportID)).
		Want(http.StatusOK).Text()
	if !strings.Contains(body, "未签发") {
		t.Error("a draft exported with no sign that it is one; that is the copy someone sends out")
	}
}

func TestTheTrailRecordsTheReportGoingOut(t *testing.T) {
	f := newAuditFixture(t)
	reportID := f.draftReport(t)
	dbfx.Cleanup(t, `DELETE FROM activity_log WHERE workspace_id = $1`, f.workspaceID)

	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusReviewing},
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusIssued},
		f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	// The two steps that happened, in order, and nothing else: a report is not
	// an issue, so these entries carry the report rather than an issue_id.
	var first, second string
	dbfx.QueryRow(t, `SELECT
	    (array_agg(action ORDER BY created_at ASC))[1],
	    (array_agg(action ORDER BY created_at ASC))[2]
	  FROM activity_log WHERE workspace_id = $1`, f.workspaceID).Scan(&first, &second)
	if first != "report_submitted" || second != "report_issued" {
		t.Errorf("trail = [%q %q], want [report_submitted report_issued]", first, second)
	}
	if n := dbfx.Count(t, `SELECT COUNT(*) FROM activity_log WHERE workspace_id = $1`, f.workspaceID); n != 2 {
		t.Errorf("trail has %d entries, want 2", n)
	}
}

func TestAnAgentCannotIssueAReport(t *testing.T) {
	f := newAuditFixture(t)
	reportID := f.draftReport(t)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusReviewing},
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)

	req := auditRequest(http.MethodPut, "/api/audit/reports/"+reportID, f.workspaceID,
		map[string]any{"status": auditreport.StatusIssued})
	req.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL2])
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", uuid.NewString())
	testutil.Call(t, testHandler.UpdateAuditReport, withURLParam(req, "reportId", reportID)).
		Want(http.StatusForbidden)
}

// A report deleted with its engagement, but the items it raised survive: a
// remediation item outlives the audit that found it.
func TestDeletingTheEngagementRemovesItsReportsAndKeepsItsItems(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	issueID := f.item(t, dept)
	f.draftReport(t)

	req := auditRequest(http.MethodDelete, "/api/projects/"+f.projectID, f.workspaceID, nil)
	testutil.Call(t, testHandler.DeleteProject, withURLParam(req, "id", f.projectID)).
		Want(http.StatusNoContent)

	if n := dbfx.Count(t, `SELECT COUNT(*) FROM audit_report WHERE project_id = $1`, f.projectID); n != 0 {
		t.Errorf("audit_report rows = %d after the engagement went, want 0", n)
	}
	if n := dbfx.Count(t, `SELECT COUNT(*) FROM issue WHERE id = $1`, issueID); n != 1 {
		t.Error("the remediation item went with the engagement; it is supposed to outlive it")
	}
}

func TestAReportStartsWithAUsableTitle(t *testing.T) {
	f := newAuditFixture(t)
	req := auditRequest(http.MethodPost, "/api/projects/"+f.projectID+"/reports", f.workspaceID,
		map[string]any{})
	req.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL1])
	var out AuditReportResponse
	testutil.Call(t, testHandler.CreateAuditReport, withURLParam(req, "id", f.projectID)).
		Want(http.StatusCreated).JSON(&out)
	if !strings.Contains(out.Title, "审计报告") {
		t.Errorf("title = %q, want the engagement's name and 审计报告", out.Title)
	}
}
