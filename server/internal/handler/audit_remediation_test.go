package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// Wiring, permissions and named regressions for the 整改台账. The transition
// matrix is the canonical property of internal/remediate and runs as a table
// there; this file does not replay it. What is here needs a database: the
// standing that closure requires, the fields an item cannot exist without, and
// the path an auditee that predates the ledger takes.

// department adds one 责任部门 to the fixture's auditee.
func (f auditFixture) department(t *testing.T, name string) string {
	t.Helper()
	req := auditRequest(http.MethodPost, "/api/audit/departments", f.workspaceID,
		map[string]any{"name": name + "-" + uuid.NewString()[:6]})
	var out AuditDepartmentResponse
	testutil.Call(t, testHandler.CreateAuditDepartment, req).Want(http.StatusCreated).JSON(&out)
	return out.ID
}

// raise creates a remediation item from the fixture's engagement, as its 主审.
func (f auditFixture) raise(t *testing.T, body map[string]any) *testutil.Response {
	t.Helper()
	req := auditRequest(http.MethodPost, "/api/projects/"+f.projectID+"/remediation", f.workspaceID, body)
	req.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL1])
	return testutil.Call(t, testHandler.RaiseRemediationItem, withURLParam(req, "id", f.projectID))
}

// item raises a well-formed remediation item owned by the shared test user and
// returns its issue id.
func (f auditFixture) item(t *testing.T, deptID string) string {
	t.Helper()
	var memberID string
	dbfx.QueryRow(t, `SELECT id::text FROM member WHERE workspace_id = $1 AND user_id = $2`,
		f.workspaceID, testUserID).Scan(&memberID)
	var out RemediationResponse
	f.raise(t, map[string]any{
		"title":         "整改事项 " + uuid.NewString()[:8],
		"department_id": deptID,
		"due_date":      time.Now().AddDate(0, 0, 14).Format("2006-01-02"),
		"assignee_id":   memberID,
	}).Want(http.StatusCreated).JSON(&out)
	return out.IssueID
}

// move drives the ordinary issue update endpoint, which is where both chains
// are enforced.
func (f auditFixture) move(t *testing.T, issueID, status, note, asUserID string) *testutil.Response {
	t.Helper()
	body := map[string]any{"status": status}
	if note != "" {
		body["review_note"] = note
	}
	req := auditRequest(http.MethodPatch, "/api/issues/"+issueID, f.workspaceID, body)
	req.Header.Set("X-User-ID", asUserID)
	return testutil.Call(t, testHandler.UpdateIssue, withURLParam(req, "id", issueID))
}

func TestARemediationItemIsRaisedOutsideTheEngagementThatFoundIt(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	issueID := f.item(t, dept)

	var projectID *string
	dbfx.QueryRow(t, `SELECT project_id::text FROM issue WHERE id = $1`, issueID).Scan(&projectID)
	if projectID != nil {
		t.Errorf("the item belongs to project %q; a remediation item belongs to no engagement, which is what lets it outlive the audit", *projectID)
	}
	var source string
	dbfx.QueryRow(t, `SELECT source_project_id::text FROM audit_remediation WHERE issue_id = $1`, issueID).Scan(&source)
	if source != f.projectID {
		t.Errorf("source engagement = %q, want %q: without it nobody is qualified to close the item", source, f.projectID)
	}
}

func TestAnItemCannotBeRaisedWithoutADepartment(t *testing.T) {
	f := newAuditFixture(t)
	f.raise(t, map[string]any{
		"title":    "没有责任部门",
		"due_date": time.Now().AddDate(0, 0, 7).Format("2006-01-02"),
	}).Want(http.StatusBadRequest)
}

func TestAnItemCannotBeRaisedWithoutADeadline(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "采购部")
	f.raise(t, map[string]any{
		"title":         "没有期限",
		"department_id": dept,
	}).Want(http.StatusBadRequest)
}

func TestADepartmentFromAnotherAuditeeIsRefused(t *testing.T) {
	f := newAuditFixture(t)
	other := newAuditWorkspace(t)
	enableAuditMode(t, other, nil).Want(http.StatusOK)
	otherDept := dbfx.Insert(t, "audit_department", testutil.Cols{
		"workspace_id": other,
		"name":         "别人的部门 " + uuid.NewString()[:6],
	})
	f.raise(t, map[string]any{
		"title":         "跨单位",
		"department_id": otherDept,
		"due_date":      time.Now().AddDate(0, 0, 7).Format("2006-01-02"),
	}).Want(http.StatusBadRequest)
}

// Raising an item asserts that this auditee owes a fix. A member who holds no
// rank on the engagement has not made that finding.
func TestRaisingAnItemNeedsARankOnTheEngagement(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "人力资源部")
	outsiderUser := dbfx.User(t, "Outsider", "out-"+uuid.NewString()[:8]+"@multica.ai")
	dbfx.Member(t, f.workspaceID, outsiderUser, "member")

	req := auditRequest(http.MethodPost, "/api/projects/"+f.projectID+"/remediation", f.workspaceID,
		map[string]any{
			"title":         "无权提出",
			"department_id": dept,
			"due_date":      time.Now().AddDate(0, 0, 7).Format("2006-01-02"),
		})
	req.Header.Set("X-User-ID", outsiderUser)
	testutil.Call(t, testHandler.RaiseRemediationItem, withURLParam(req, "id", f.projectID)).
		Want(http.StatusForbidden)
}

func TestTheLedgerRunsFromStartToVerifiedClosure(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	issueID := f.item(t, dept)

	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusPendingVerification, "补签了三份合同", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusRemediationClosed, "抽查了三份，已补签",
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)

	if got := f.statusOf(t, issueID); got != auditmode.StatusRemediationClosed {
		t.Fatalf("status = %q, want the item closed", got)
	}
	var verifier, note string
	dbfx.QueryRow(t, `SELECT verified_by::text, verification_note FROM audit_remediation WHERE issue_id = $1`, issueID).
		Scan(&verifier, &note)
	if verifier != f.reviewers[auditgate.LevelL1] {
		t.Errorf("verified_by = %q, want the verifying member %q", verifier, f.reviewers[auditgate.LevelL1])
	}
	if note == "" {
		t.Error("the closure recorded no account of what was checked; a closure with none proves nothing later")
	}
}

// The rule the whole ledger rests on, enforced through the real write path
// rather than only in the matrix.
func TestTheResponsiblePersonCannotCloseTheirOwnItemThroughTheAPI(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	// The responsible person is also a reviewer on the engagement — the case
	// where a rank would otherwise be enough.
	var memberID string
	dbfx.QueryRow(t, `SELECT id::text FROM member WHERE workspace_id = $1 AND user_id = $2`,
		f.workspaceID, f.reviewerUsers[auditgate.LevelL2]).Scan(&memberID)
	var out RemediationResponse
	f.raise(t, map[string]any{
		"title":         "自查自纠",
		"department_id": dept,
		"due_date":      time.Now().AddDate(0, 0, 7).Format("2006-01-02"),
		"assignee_id":   memberID,
	}).Want(http.StatusCreated).JSON(&out)

	responsible := f.reviewerUsers[auditgate.LevelL2]
	f.move(t, out.IssueID, auditmode.StatusRemediating, "", responsible).Want(http.StatusOK)
	f.move(t, out.IssueID, auditmode.StatusPendingVerification, "已整改", responsible).Want(http.StatusOK)
	f.move(t, out.IssueID, auditmode.StatusRemediationClosed, "自己看过了", responsible).
		Want(http.StatusForbidden)

	if got := f.statusOf(t, out.IssueID); got != auditmode.StatusPendingVerification {
		t.Errorf("status = %q, want the refused closure to have changed nothing", got)
	}
}

// The rank has to be on the engagement that RAISED the item. A reviewer on some
// other engagement is not the audit function for this finding.
func TestAVerifierNeedsARankOnTheRaisingEngagement(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "信息部")
	issueID := f.item(t, dept)
	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusPendingVerification, "已整改", testUserID).Want(http.StatusOK)

	// A reviewer, but on a different engagement in the same auditee.
	otherProject := dbfx.Insert(t, "project", testutil.Cols{
		"workspace_id": f.workspaceID,
		"title":        "另一个项目 " + uuid.NewString()[:8],
	})
	strangerUser := dbfx.User(t, "Stranger", "str-"+uuid.NewString()[:8]+"@multica.ai")
	strangerMember := dbfx.Member(t, f.workspaceID, strangerUser, "member")
	dbfx.Insert(t, "audit_role", testutil.Cols{
		"workspace_id": f.workspaceID,
		"project_id":   otherProject,
		"member_id":    strangerMember,
		"level":        string(auditgate.LevelL1),
	})

	f.move(t, issueID, auditmode.StatusRemediationClosed, "我看过了", strangerUser).
		Want(http.StatusForbidden)
}

func TestClosingWithoutAnAccountOfWhatWasCheckedIsRefused(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "工程部")
	issueID := f.item(t, dept)
	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusPendingVerification, "已整改", testUserID).Want(http.StatusOK)

	f.move(t, issueID, auditmode.StatusRemediationClosed, "", f.reviewerUsers[auditgate.LevelL1]).
		Want(http.StatusBadRequest)
}

func TestAClosedItemRefusesFurtherWritesThroughTheAPI(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	issueID := f.item(t, dept)
	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusPendingVerification, "已整改", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusRemediationClosed, "抽查通过", f.reviewerUsers[auditgate.LevelL1]).
		Want(http.StatusOK)

	f.move(t, issueID, auditmode.StatusRemediating, "再改一次", f.reviewerUsers[auditgate.LevelL1]).
		Want(http.StatusConflict)
}

// 待验证 -> done would close the item with nobody verifying anything, which is
// the whole failure the chain prevents.
func TestAnItemCannotBeMarkedDoneOutsideTheLedger(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	issueID := f.item(t, dept)
	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)

	f.move(t, issueID, issuestatus.Done, "", testUserID).Want(http.StatusConflict)
	if got := f.statusOf(t, issueID); got != auditmode.StatusRemediating {
		t.Errorf("status = %q, want the refused write to have changed nothing", got)
	}
}

// An ordinary issue put on a remediation status would be an item with no
// department, no deadline, and no engagement whose ranks could close it.
func TestAnOrdinaryIssueCannotBePutOnTheLedgerByStatusAlone(t *testing.T) {
	f := newAuditFixture(t)
	ordinary := dbfx.Issue(t, "普通任务 "+uuid.NewString()[:8], testutil.Cols{
		"workspace_id": f.workspaceID,
		"status":       issuestatus.Todo,
	})
	f.move(t, ordinary, auditmode.StatusRemediating, "", testUserID).Want(http.StatusConflict)
}

// The glossary's claim that a remediation item outlives the audit that found it
// is true because it belongs to no engagement. Dragging one into an engagement
// would quietly make it a workpaper.
func TestARemediationItemCannotBeMovedIntoAnEngagement(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	issueID := f.item(t, dept)
	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)

	req := auditRequest(http.MethodPatch, "/api/issues/"+issueID, f.workspaceID,
		map[string]any{"project_id": f.projectID})
	req.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL1])
	testutil.Call(t, testHandler.UpdateIssue, withURLParam(req, "id", issueID)).Want(http.StatusConflict)
}

func TestADepartmentWithItemsCannotBeRemoved(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	f.item(t, dept)

	req := auditRequest(http.MethodDelete, "/api/audit/departments/"+dept, f.workspaceID, nil)
	testutil.Call(t, testHandler.DeleteAuditDepartment, withURLParam(req, "id", dept)).
		Want(http.StatusConflict)
}

func TestADepartmentThatOwesNothingCanBeRemoved(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "临时部门")
	req := auditRequest(http.MethodDelete, "/api/audit/departments/"+dept, f.workspaceID, nil)
	testutil.Call(t, testHandler.DeleteAuditDepartment, withURLParam(req, "id", dept)).
		Want(http.StatusNoContent)
}

func TestTwoSpellingsOfOneDepartmentAreRefused(t *testing.T) {
	f := newAuditFixture(t)
	name := "财务部-" + uuid.NewString()[:6]
	create := func(n string) *testutil.Response {
		return testutil.Call(t, testHandler.CreateAuditDepartment,
			auditRequest(http.MethodPost, "/api/audit/departments", f.workspaceID, map[string]any{"name": n}))
	}
	create(name).Want(http.StatusCreated)
	// Counting by department is the reason the list exists; two rows for one
	// department make every count quietly wrong.
	create(name).Want(http.StatusConflict)
}

func TestTheLedgerReportsOverdueWithoutStoringIt(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	issueID := f.item(t, dept)
	dbfx.Exec(t, `UPDATE issue SET due_date = CURRENT_DATE - 5 WHERE id = $1`, issueID)

	req := auditRequest(http.MethodGet, "/api/audit/remediation?overdue=true", f.workspaceID, nil)
	var rows []RemediationResponse
	testutil.Call(t, testHandler.ListRemediationLedger, req).Want(http.StatusOK).JSON(&rows)

	var found *RemediationResponse
	for i := range rows {
		if rows[i].IssueID == issueID {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatal("an item five days past its deadline is missing from the overdue ledger")
	}
	if !found.Overdue || found.DaysLate != 5 {
		t.Errorf("overdue = %v, days_late = %d, want true and 5", found.Overdue, found.DaysLate)
	}
}

func TestAClosedItemIsNotLate(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	issueID := f.item(t, dept)
	dbfx.Exec(t, `UPDATE issue SET due_date = CURRENT_DATE - 5 WHERE id = $1`, issueID)
	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusPendingVerification, "已整改", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusRemediationClosed, "抽查通过", f.reviewerUsers[auditgate.LevelL1]).
		Want(http.StatusOK)

	req := auditRequest(http.MethodGet, "/api/audit/remediation", f.workspaceID, nil)
	var rows []RemediationResponse
	testutil.Call(t, testHandler.ListRemediationLedger, req).Want(http.StatusOK).JSON(&rows)
	for _, row := range rows {
		if row.IssueID == issueID && row.Overdue {
			t.Error("a closed item is reported as late; it is finished, whenever it finished")
		}
	}
}

// THE path, not the mechanism. An auditee enabled before the ledger existed has
// no 整改中 in its catalog, and the enable endpoint returns early once a
// workspace is an auditee — so without a convergent seed the feature silently
// does not exist for every workspace that adopted the vertical early.
func TestAnAuditeeThatPredatesTheLedgerReceivesItsStatuses(t *testing.T) {
	f := newAuditFixture(t)
	// Exactly what such a workspace looks like: an auditee whose catalog holds
	// the review chain and nothing else.
	dbfx.Exec(t, `DELETE FROM issue_status WHERE workspace_id = $1 AND key = ANY($2)`,
		f.workspaceID, []string{
			auditmode.StatusRemediating,
			auditmode.StatusPendingVerification,
			auditmode.StatusRemediationClosed,
		})

	enableAuditMode(t, f.workspaceID, nil).Want(http.StatusOK)

	for _, key := range []string{
		auditmode.StatusRemediating,
		auditmode.StatusPendingVerification,
		auditmode.StatusRemediationClosed,
	} {
		if n := dbfx.Count(t, `SELECT COUNT(*) FROM issue_status WHERE workspace_id = $1 AND key = $2`,
			f.workspaceID, key); n != 1 {
			t.Errorf("status %q rows = %d, want 1", key, n)
		}
	}

	// And the ledger works end to end for it, which is the thing the statuses
	// were missing for.
	dept := f.department(t, "财务部")
	issueID := f.item(t, dept)
	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)
}

func TestTheActionsEndpointOffersLedgerStepsToo(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	issueID := f.item(t, dept)
	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusPendingVerification, "已整改", testUserID).Want(http.StatusOK)

	req := auditRequest(http.MethodGet, "/api/issues/"+issueID+"/audit-actions", f.workspaceID, nil)
	req.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL1])
	var actions []AuditActionResponse
	testutil.Call(t, testHandler.ListAuditActions, withURLParam(req, "id", issueID)).
		Want(http.StatusOK).JSON(&actions)

	offered := map[string]bool{}
	for _, a := range actions {
		offered[a.To] = true
	}
	if !offered[auditmode.StatusRemediationClosed] {
		t.Errorf("the verifier was offered %v, want closure among them", actions)
	}
}

// A mis-routed item is corrected, not recreated: it keeps its history, its
// deadline and its place in the ledger.
func TestAnItemCanBeReassignedToAnotherDepartment(t *testing.T) {
	f := newAuditFixture(t)
	first := f.department(t, "财务部")
	second := f.department(t, "采购部")
	issueID := f.item(t, first)

	req := auditRequest(http.MethodPut, "/api/issues/"+issueID+"/remediation", f.workspaceID,
		map[string]any{"department_id": second})
	req.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL1])
	var out RemediationResponse
	testutil.Call(t, testHandler.UpdateRemediationDepartment, withURLParam(req, "id", issueID)).
		Want(http.StatusOK).JSON(&out)
	if out.DepartmentID != second {
		t.Errorf("department = %q, want %q", out.DepartmentID, second)
	}
}

func TestTheTrailRecordsEveryLedgerStep(t *testing.T) {
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	issueID := f.item(t, dept)
	dbfx.Cleanup(t, `DELETE FROM activity_log WHERE issue_id = $1`, issueID)

	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusPendingVerification, "已整改", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusRemediationClosed, "抽查通过", f.reviewerUsers[auditgate.LevelL1]).
		Want(http.StatusOK)

	want := []string{"remediation_started", "remediation_submitted", "remediation_verified"}
	got := f.trailFor(t, issueID)
	if len(got) != len(want) {
		t.Fatalf("trail has %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Action != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i].Action, want[i])
		}
	}
}
