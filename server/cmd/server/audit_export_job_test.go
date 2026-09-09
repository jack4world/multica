package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auditexport"
	"github.com/multica-ai/multica/server/internal/scheduler"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// The export job against a real database. The bundle FORMAT is settled in
// internal/auditexport without one; what needs a database is the job's own
// behaviour: which workspaces it touches, what it reads, and the order it
// writes in.

type recordingStore struct {
	objects map[string][]byte
	order   []string
}

func newRecordingStore() *recordingStore {
	return &recordingStore{objects: map[string][]byte{}}
}

func (s *recordingStore) Upload(_ context.Context, key string, data []byte, _, _ string) (string, error) {
	stored := make([]byte, len(data))
	copy(stored, data)
	s.objects[key] = stored
	s.order = append(s.order, key)
	return "file://" + key, nil
}
func (s *recordingStore) Delete(context.Context, string)             {}
func (s *recordingStore) DeleteObject(context.Context, string) error { return nil }
func (s *recordingStore) DeleteKeys(context.Context, []string)       {}
func (s *recordingStore) KeyFromURL(string) string                   { return "" }
func (s *recordingStore) ObjectURL(key string) string                { return key }
func (s *recordingStore) CdnDomain() string                          { return "" }
func (s *recordingStore) GetReader(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

// runExportForDay drives the real job handler for the day that ended at
// planTime, and returns what it wrote.
func runExportForDay(t *testing.T, planTime time.Time) *recordingStore {
	t.Helper()
	store := newRecordingStore()
	spec := scheduler.AuditTrailExportJob(db.New(testPool), store)
	if _, err := spec.Handler(context.Background(), scheduler.HandlerInput{
		Job:       &spec,
		PlanTime:  planTime,
		Heartbeat: func(context.Context) error { return nil },
	}); err != nil {
		t.Fatalf("export: %v", err)
	}
	return store
}

// newExportAuditee creates an auditee with one trail entry inside the window.
func newExportAuditee(t *testing.T, at time.Time) string {
	t.Helper()
	ctx := context.Background()
	slug := "export-" + uuid.NewString()[:8]
	var wsID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix, audit_mode_enabled_at)
		 VALUES ($1, $1, '', '', now()) RETURNING id::text`, slug).Scan(&wsID); err != nil {
		t.Fatalf("create auditee: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM activity_log WHERE workspace_id = $1`, wsID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID)
	})
	if _, err := testPool.Exec(ctx,
		`INSERT INTO activity_log (id, workspace_id, actor_type, action, details, created_at)
		 VALUES ($1, $2, 'member', 'workpaper_filed', '{"level":"reviewer_l3"}'::jsonb, $3)`,
		dbid.NewV7(), wsID, at); err != nil {
		t.Fatalf("seed trail: %v", err)
	}
	return wsID
}

// The manifest goes last. A reader treats a day with no manifest as unfinished,
// so writing it first would let a crash between the two uploads present a
// truncated day as complete.
func TestTheExportWritesTheManifestAfterTheRecords(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	dayEnd := time.Now().UTC().Truncate(24 * time.Hour)
	wsID := newExportAuditee(t, dayEnd.Add(-6*time.Hour))

	store := runExportForDay(t, dayEnd)

	var seen []string
	for _, key := range store.order {
		if strings.Contains(key, wsID) {
			seen = append(seen, key)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("wrote %d objects for the auditee, want 2: %v", len(seen), seen)
	}
	if !strings.HasSuffix(seen[0], "records.jsonl") || !strings.HasSuffix(seen[1], "manifest.json") {
		t.Errorf("upload order = %v, want records then manifest", seen)
	}
}

func TestTheExportedManifestCountsWhatTheFileHolds(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	dayEnd := time.Now().UTC().Truncate(24 * time.Hour)
	wsID := newExportAuditee(t, dayEnd.Add(-6*time.Hour))

	store := runExportForDay(t, dayEnd)

	var m auditexport.Manifest
	for key, body := range store.objects {
		if strings.Contains(key, wsID) && strings.HasSuffix(key, "manifest.json") {
			if err := json.Unmarshal(body, &m); err != nil {
				t.Fatalf("manifest: %v", err)
			}
		}
	}
	if m.RecordCount != 1 {
		t.Errorf("record_count = %d, want 1", m.RecordCount)
	}
	data := store.objects[m.DataKey]
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != m.RecordCount {
		t.Errorf("manifest says %d records, file holds %d", m.RecordCount, lines)
	}
}

// The whole point of the export is that the record survives. A job that could
// lose one while making it durable would be self-defeating.
func TestTheExportNeverRemovesWhatItCopied(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	dayEnd := time.Now().UTC().Truncate(24 * time.Hour)
	wsID := newExportAuditee(t, dayEnd.Add(-6*time.Hour))

	runExportForDay(t, dayEnd)

	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM activity_log WHERE workspace_id = $1`, wsID).Scan(&count); err != nil {
		t.Fatalf("count trail: %v", err)
	}
	if count != 1 {
		t.Errorf("trail rows after export = %d, want 1 — the export copies, it never prunes", count)
	}
}

// Re-running a day after a failure must be safe, and must be distinguishable
// from someone having altered the file.
func TestReRunningADayProducesIdenticalBytes(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	dayEnd := time.Now().UTC().Truncate(24 * time.Hour)
	newExportAuditee(t, dayEnd.Add(-6*time.Hour))

	first := runExportForDay(t, dayEnd)
	second := runExportForDay(t, dayEnd)

	for key, body := range first.objects {
		if string(second.objects[key]) != string(body) {
			t.Errorf("re-running the day changed %s; an operator cannot tell a retry from a tampered file", key)
		}
	}
}

// An ordinary deployment runs this job too. It must do nothing.
func TestTheExportIgnoresWorkspacesThatAreNotAuditees(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	ctx := context.Background()
	slug := "plain-" + uuid.NewString()[:8]
	var wsID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix)
		 VALUES ($1, $1, '', '') RETURNING id::text`, slug).Scan(&wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID) })

	store := runExportForDay(t, time.Now().UTC().Truncate(24*time.Hour))

	for key := range store.objects {
		if strings.Contains(key, wsID) {
			t.Errorf("exported %s for a workspace that is not an auditee", key)
		}
	}
}
