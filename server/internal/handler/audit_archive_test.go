package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

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
