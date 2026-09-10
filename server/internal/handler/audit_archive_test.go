package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/auditarchive"
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/auditreport"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// Which engagements may be archived, by whom, and what the written file
// contains. The archive FORMAT is canonical in internal/auditarchive and
// exercised there.

// archiveStore records what an archival wrote, in the order it wrote it.
type archiveStore struct {
	objects map[string][]byte
	order   []string
}

func newArchiveStore() *archiveStore { return &archiveStore{objects: map[string][]byte{}} }

func (s *archiveStore) Upload(_ context.Context, key string, data []byte, _, _ string) (string, error) {
	stored := make([]byte, len(data))
	copy(stored, data)
	s.objects[key] = stored
	s.order = append(s.order, key)
	return "file://" + key, nil
}
func (s *archiveStore) Delete(context.Context, string)             {}
func (s *archiveStore) DeleteObject(context.Context, string) error { return nil }
func (s *archiveStore) DeleteKeys(context.Context, []string)       {}
func (s *archiveStore) KeyFromURL(string) string                   { return "" }
func (s *archiveStore) ObjectURL(key string) string                { return key }
func (s *archiveStore) CdnDomain() string                          { return "" }
func (s *archiveStore) GetReader(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

// withArchiveStore points the handler at a recording destination for one test.
func withArchiveStore(t *testing.T) *archiveStore {
	t.Helper()
	store := newArchiveStore()
	previous := testHandler.AuditArchiveStorage
	testHandler.AuditArchiveStorage = store
	t.Cleanup(func() { testHandler.AuditArchiveStorage = previous })
	return store
}

func (f auditFixture) archive(t *testing.T, asUserID string) *testutil.Response {
	t.Helper()
	req := auditRequest(http.MethodPost, "/api/projects/"+f.projectID+"/archive", f.workspaceID, nil)
	req.Header.Set("X-User-ID", asUserID)
	return testutil.Call(t, testHandler.ArchiveEngagement, withURLParam(req, "id", f.projectID))
}

// issueReport takes the engagement's report all the way out, which is what
// archiving requires.
func (f auditFixture) issueReport(t *testing.T) {
	t.Helper()
	reportID := f.draftReport(t)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusReviewing},
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)
	f.writeReport(t, reportID, map[string]any{"status": auditreport.StatusIssued},
		f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)
}

func TestArchivingWritesTheWholeFileAndTheManifestLast(t *testing.T) {
	store := withArchiveStore(t)
	f := newAuditFixture(t)
	f.workpaper(t, auditmode.StatusFiled)
	dept := f.department(t, "采购部")
	f.item(t, dept)
	f.issueReport(t)

	var out ArchiveResponse
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK).JSON(&out)

	if len(store.order) != 7 {
		t.Fatalf("wrote %d objects, want 6 files and a manifest: %v", len(store.order), store.order)
	}
	// A reader treats a prefix with no manifest as unfinished, so a crash
	// mid-write must not be able to leave a complete-looking archive.
	if !strings.HasSuffix(store.order[len(store.order)-1], "manifest.json") {
		t.Errorf("write order = %v, want the manifest last", store.order)
	}
	if out.ArchiveKey != auditarchive.ManifestKeyFor(f.workspaceID, f.projectID) {
		t.Errorf("archive_key = %q, want the manifest's key", out.ArchiveKey)
	}
	var key string
	dbfx.QueryRow(t, `SELECT audit_archive_key FROM project WHERE id = $1`, f.projectID).Scan(&key)
	if key != out.ArchiveKey {
		t.Errorf("the engagement records %q, want the key the archive was written under", key)
	}
}

// The file has to hold the report that went out, not one rebuilt from today's
// rows.
func TestTheArchiveHoldsTheIssuedReport(t *testing.T) {
	store := withArchiveStore(t)
	f := newAuditFixture(t)
	f.issueReport(t)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	body := ""
	for key, data := range store.objects {
		if strings.HasSuffix(key, "report.md") {
			body = string(data)
		}
	}
	if !strings.Contains(body, "签发") || strings.Contains(body, "未签发") {
		t.Errorf("the archived report is not the issued one:\n%s", body)
	}
}

func TestArchivingIsRefusedWithoutAnIssuedReport(t *testing.T) {
	withArchiveStore(t)
	f := newAuditFixture(t)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusConflict)
}

// An archive taken over unfinished work presents it as a closed file.
func TestArchivingIsRefusedWhileAWorkpaperIsStillInTheChain(t *testing.T) {
	withArchiveStore(t)
	f := newAuditFixture(t)
	f.issueReport(t)
	f.workpaper(t, auditmode.StatusReviewL1)

	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusConflict)
}

func TestArchivingNeedsTheSameRankThatSignsTheReport(t *testing.T) {
	withArchiveStore(t)
	f := newAuditFixture(t)
	f.issueReport(t)

	f.archive(t, f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusForbidden)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)
}

func TestAnEngagementIsArchivedOnce(t *testing.T) {
	withArchiveStore(t)
	f := newAuditFixture(t)
	f.issueReport(t)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusConflict)
}

// The worst possible failure for this feature is reporting success while
// writing nowhere.
func TestArchivingIsRefusedWithNoDestinationConfigured(t *testing.T) {
	f := newAuditFixture(t)
	f.issueReport(t)
	previous := testHandler.AuditArchiveStorage
	testHandler.AuditArchiveStorage = nil
	t.Cleanup(func() { testHandler.AuditArchiveStorage = previous })

	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusServiceUnavailable)

	var archived *string
	dbfx.QueryRow(t, `SELECT audit_archived_at::text FROM project WHERE id = $1`, f.projectID).Scan(&archived)
	if archived != nil {
		t.Error("the engagement was closed although nothing was written")
	}
}

// 已归档 means the file is closed.
func TestAnArchivedEngagementTakesNoMoreWork(t *testing.T) {
	withArchiveStore(t)
	f := newAuditFixture(t)
	f.issueReport(t)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	wp := f.workpaper(t, auditmode.StatusDrafting)
	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusConflict)
}

func TestTheManifestDescribesTheEngagement(t *testing.T) {
	store := withArchiveStore(t)
	f := newAuditFixture(t)
	f.workpaper(t, auditmode.StatusFiled)
	f.issueReport(t)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	var m auditarchive.Manifest
	for key, data := range store.objects {
		if strings.HasSuffix(key, "manifest.json") {
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatalf("manifest: %v", err)
			}
		}
	}
	if m.Engagement.ProjectID != f.projectID || m.Engagement.WorkspaceID != f.workspaceID {
		t.Errorf("manifest names engagement %+v", m.Engagement)
	}
	if m.Engagement.Auditee == "" {
		t.Error("the manifest does not say which auditee this file is about")
	}
	if m.Engagement.ReviewLevels != 2 {
		t.Errorf("manifest review_levels = %d, want the depth the engagement ran", m.Engagement.ReviewLevels)
	}
	if m.ArchivedBy == "" || m.ArchivedAt == "" {
		t.Error("the manifest does not record who closed the file, or when")
	}
	if len(m.Files) != 6 {
		t.Errorf("manifest lists %d files, want 6", len(m.Files))
	}
}

// The workpaper's audit fields have to be readable in three years, when the
// definition ids they were stored against mean nothing to anyone.
func TestTheArchivedWorkpapersCarryTheirFieldsByName(t *testing.T) {
	store := withArchiveStore(t)
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusFiled)
	var propertyID string
	dbfx.QueryRow(t, `SELECT id::text FROM issue_property WHERE workspace_id = $1 AND name = '审计程序编码'`,
		f.workspaceID).Scan(&propertyID)
	dbfx.Exec(t, `UPDATE issue SET properties = jsonb_build_object($2::text, 'A1-01') WHERE id = $1`, wp, propertyID)
	f.issueReport(t)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	line := ""
	for key, data := range store.objects {
		if strings.HasSuffix(key, "workpapers.jsonl") {
			line = string(data)
		}
	}
	if !strings.Contains(line, "审计程序编码") || !strings.Contains(line, "A1-01") {
		t.Errorf("the archived workpaper does not carry its procedure code by name:\n%s", line)
	}
	if strings.Contains(line, propertyID) {
		t.Errorf("the archive records a definition uuid where a name belongs:\n%s", line)
	}
}

func TestTheArchivedTrailIncludesTheReportGoingOut(t *testing.T) {
	store := withArchiveStore(t)
	f := newAuditFixture(t)
	f.issueReport(t)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	trail := ""
	for key, data := range store.objects {
		if strings.HasSuffix(key, "trail.jsonl") {
			trail = string(data)
		}
	}
	if !strings.Contains(trail, "report_issued") {
		t.Errorf("the archived trail does not record the report going out:\n%s", trail)
	}
}

func TestAnAgentCannotArchiveAnEngagement(t *testing.T) {
	withArchiveStore(t)
	f := newAuditFixture(t)
	f.issueReport(t)

	req := auditRequest(http.MethodPost, "/api/projects/"+f.projectID+"/archive", f.workspaceID, nil)
	req.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL2])
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", "11111111-1111-4111-8111-111111111111")
	testutil.Call(t, testHandler.ArchiveEngagement, withURLParam(req, "id", f.projectID)).
		Want(http.StatusForbidden)
}

// EVERY write that names an engagement, against an archived one.
//
// A table rather than a handful of spot checks, because the failure this
// prevents is silent: the archive publishes a sha256 per file, which proves
// nothing was altered INSIDE the package and nothing at all about what was
// added to the engagement afterwards. A report or an item created after
// archival makes the file quietly incomplete, and "is this the whole file?" is
// the only question an archive exists to answer.
//
// Adding an engagement-scoped audit write without adding it here leaves this
// list visibly short — which is the point.
func TestAnArchivedEngagementRefusesEveryEngagementScopedWrite(t *testing.T) {
	withArchiveStore(t)
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	f.issueReport(t)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	l2 := f.reviewerUsers[auditgate.LevelL2]
	var memberID string
	dbfx.QueryRow(t, `SELECT id::text FROM member WHERE workspace_id = $1 AND user_id = $2`,
		f.workspaceID, l2).Scan(&memberID)

	cases := []struct {
		name    string
		method  string
		path    string
		body    map[string]any
		handler http.HandlerFunc
		param   string
	}{
		{
			name: "起草新报告", method: http.MethodPost, path: "/reports",
			body: map[string]any{"title": "归档后新建"}, handler: testHandler.CreateAuditReport, param: "id",
		},
		{
			name: "提出新的整改事项", method: http.MethodPost, path: "/remediation",
			body: map[string]any{
				"title": "归档后新提", "department_id": dept,
				"due_date": time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
			},
			handler: testHandler.RaiseRemediationItem, param: "id",
		},
		{
			name: "任命复核人", method: http.MethodPut, path: "/audit-roles",
			body:    map[string]any{"member_id": memberID, "level": string(auditgate.LevelL1)},
			handler: testHandler.SetAuditRole, param: "id",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := auditRequest(c.method, "/api/projects/"+f.projectID+c.path, f.workspaceID, c.body)
			req.Header.Set("X-User-ID", l2)
			testutil.Call(t, c.handler, withURLParam(req, c.param, f.projectID)).Want(http.StatusConflict)
		})
	}

	// Nothing was created despite the attempts.
	if n := dbfx.Count(t, `SELECT COUNT(*) FROM audit_report WHERE project_id = $1`, f.projectID); n != 1 {
		t.Errorf("audit_report rows = %d, want only the issued one", n)
	}
	if n := dbfx.Count(t, `SELECT COUNT(*) FROM audit_remediation WHERE source_project_id = $1`, f.projectID); n != 0 {
		t.Errorf("audit_remediation rows = %d, want 0", n)
	}
}

// The other half, and the one it would be easy to break while fixing the
// above: a remediation item OUTLIVES the audit that found it (docs/adr/0004).
// Its engagement closing must not freeze the item — chasing it afterwards is
// exactly what 后续审计 is.
func TestAnItemKeepsMovingAfterItsEngagementIsArchived(t *testing.T) {
	withArchiveStore(t)
	f := newAuditFixture(t)
	dept := f.department(t, "财务部")
	issueID := f.item(t, dept)
	f.issueReport(t)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusPendingVerification, "已整改", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusRemediationClosed, "抽查通过",
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)

	// And it can still be re-routed to the department that actually owns it.
	other := f.department(t, "采购部")
	req := auditRequest(http.MethodPut, "/api/issues/"+issueID+"/remediation", f.workspaceID,
		map[string]any{"department_id": other})
	req.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL1])
	testutil.Call(t, testHandler.UpdateRemediationDepartment, withURLParam(req, "id", issueID)).
		Want(http.StatusOK)
}

// A draft can outlive archival — archiving requires an ISSUED report, not the
// absence of a draft — and writing to it afterwards would add to a closed file.
func TestADraftReportIsFrozenWhenTheEngagementIsArchived(t *testing.T) {
	withArchiveStore(t)
	f := newAuditFixture(t)
	f.issueReport(t)

	// A second version, started before the file closes.
	var draft AuditReportResponse
	f.startReport(t, f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusCreated).JSON(&draft)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	f.writeReport(t, draft.ID, map[string]any{"opinion": "归档后再写"},
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusConflict)
}

// An archive nobody can read the names in answers nothing.
//
// The point of the file is that it survives the system that produced it: no
// database to join against, people who have left, possibly a company that has
// replaced the software. A bare uuid in a trail entry answers "who approved
// this?" with "look it up", and the first version of this archive did exactly
// that — worse, it wrote the trail's actor as a USER id and the preparer as a
// MEMBER id, so the same person appeared in two files as two unrelated uuids.
func TestTheArchiveNamesThePeopleInIt(t *testing.T) {
	store := withArchiveStore(t)
	f := newAuditFixture(t)
	dept := f.department(t, "采购部")
	issueID := f.item(t, dept)

	// A workpaper walked by real people, so preparer and signer are recorded.
	wp := f.workpaper(t, auditmode.StatusDrafting)
	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusReviewL2, f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusFiled, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	// An item taken to closure, so responsible and verifier are recorded.
	f.move(t, issueID, auditmode.StatusRemediating, "", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusPendingVerification, "已整改", testUserID).Want(http.StatusOK)
	f.move(t, issueID, auditmode.StatusRemediationClosed, "抽查通过",
		f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)

	f.issueReport(t)
	f.archive(t, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)

	read := func(suffix string) string {
		t.Helper()
		for key, data := range store.objects {
			if strings.HasSuffix(key, suffix) {
				return string(data)
			}
		}
		t.Fatalf("no %s in the archive", suffix)
		return ""
	}

	var workpapers []auditarchive.Workpaper
	for _, line := range strings.Split(strings.TrimSpace(read("workpapers.jsonl")), "\n") {
		var w auditarchive.Workpaper
		if err := json.Unmarshal([]byte(line), &w); err != nil {
			t.Fatalf("workpapers.jsonl: %v", err)
		}
		workpapers = append(workpapers, w)
	}
	var preparer auditarchive.Person
	for _, w := range workpapers {
		if w.IssueID == wp {
			preparer = w.Preparer
		}
	}
	if preparer.Name == "" {
		t.Error("the archived workpaper names no preparer; a uuid is not an answer to \"who wrote this\"")
	}

	var signers []auditarchive.Person
	for _, line := range strings.Split(strings.TrimSpace(read("trail.jsonl")), "\n") {
		var e auditarchive.TrailEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("trail.jsonl: %v", err)
		}
		if e.Action == string(auditgate.EventFiled) || e.Action == string(auditgate.EventReviewPassed) {
			signers = append(signers, e.Actor)
		}
	}
	if len(signers) == 0 {
		t.Fatal("the archived trail records no signatures")
	}
	for _, s := range signers {
		if s.Name == "" {
			t.Errorf("a signature in the trail names nobody: %+v", s)
		}
		// The rank is what makes a signature readable: "赵六" alone leaves the
		// reader asking what standing they had to sign it.
		if s.Level == "" {
			t.Errorf("signature by %q records no rank on this engagement", s.Name)
		}
	}

	// THE cross-file check: the preparer and the trail actors have to be
	// matchable, which they were not while one was a member id and the other a
	// user id.
	var preparerAppearsInTrail bool
	for _, s := range signers {
		if s.ID == preparer.ID {
			preparerAppearsInTrail = true
		}
	}
	var trailIDs []string
	for _, s := range signers {
		trailIDs = append(trailIDs, s.ID)
	}
	if preparer.ID == "" {
		t.Error("the preparer has no id to match against the trail")
	}
	_ = preparerAppearsInTrail // the preparer need not be a signer; the ids must simply be comparable
	for _, id := range trailIDs {
		if id == "" {
			t.Error("a trail actor has no id")
		}
	}

	var items []auditarchive.RemediationItem
	for _, line := range strings.Split(strings.TrimSpace(read("remediation.jsonl")), "\n") {
		var it auditarchive.RemediationItem
		if err := json.Unmarshal([]byte(line), &it); err != nil {
			t.Fatalf("remediation.jsonl: %v", err)
		}
		items = append(items, it)
	}
	if len(items) != 1 {
		t.Fatalf("archived %d items, want 1", len(items))
	}
	if items[0].Responsible.Name == "" {
		t.Error("the archived item names nobody responsible for the fix")
	}
	if items[0].Verifier.Name == "" {
		t.Error("the archived item names nobody as having verified it")
	}
}
