package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/auditexport"
)

// fakeStore records what the export wrote without needing object storage.
type fakeStore struct {
	objects map[string][]byte
	order   []string
}

func newFakeStore() *fakeStore { return &fakeStore{objects: map[string][]byte{}} }

func (f *fakeStore) Upload(_ context.Context, key string, data []byte, _ string, _ string) (string, error) {
	stored := make([]byte, len(data))
	copy(stored, data)
	f.objects[key] = stored
	f.order = append(f.order, key)
	return "https://example.invalid/" + key, nil
}
func (f *fakeStore) Delete(context.Context, string)             {}
func (f *fakeStore) DeleteObject(context.Context, string) error { return nil }
func (f *fakeStore) DeleteKeys(context.Context, []string)       {}
func (f *fakeStore) KeyFromURL(string) string                   { return "" }
func (f *fakeStore) ObjectURL(key string) string                { return key }
func (f *fakeStore) CdnDomain() string                          { return "" }
func (f *fakeStore) GetReader(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

// The job spec is a contract with the scheduler, and two of its settings carry
// meaning that would be lost if someone "tidied" them.
func TestTheExportReplaysEveryMissedDayNotJustTheLatest(t *testing.T) {
	spec := AuditTrailExportJob(nil, nil)
	if spec.CatchUpMode != CatchUpEveryPlan {
		t.Error("a skipped day is a hole in the record; catching up on only the latest plan " +
			"would silently turn two missed days into one exported day")
	}
	if spec.Cadence != 24*time.Hour {
		t.Errorf("cadence = %v, want a day", spec.Cadence)
	}
	if !spec.AllowStaleReentry {
		t.Error("re-running a day overwrites the same keys with identical content, so a stale " +
			"lease is safe to steal; refusing would require manual repair for nothing")
	}
}

func TestTheExportIsInertWithoutStorage(t *testing.T) {
	handler := makeAuditTrailExportHandler(nil, nil)
	res, err := handler(context.Background(), HandlerInput{
		PlanTime:  time.Now().UTC(),
		Heartbeat: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("no configured storage should be a quiet no-op, got: %v", err)
	}
	if res.RowsAffected != 0 {
		t.Errorf("rows = %d, want 0", res.RowsAffected)
	}
}

// The ordering of the two uploads is asserted in the handler test in
// cmd/server, which drives the real job against a database. A version of this
// test used to live here and performed the two uploads itself, in the order it
// then checked — so swapping the two lines in the job left it green. A guard
// that cannot fail is worse than no guard, because it stops anyone writing one
// that can.

// The count in the manifest is what makes a truncated export detectable, so it
// has to describe the file that was actually written.
func TestTheManifestCountMatchesTheRecordsWritten(t *testing.T) {
	records := []auditexport.Record{
		{ID: "1", Action: "workpaper_submitted", At: time.Now().UTC()},
		{ID: "2", Action: "workpaper_filed", At: time.Now().UTC().Add(time.Minute)},
	}
	bundle := auditexport.Build("ws-1", time.Now().UTC().Truncate(24*time.Hour), records)

	var m auditexport.Manifest
	if err := json.Unmarshal(bundle.Manifest, &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	lines := strings.Count(strings.TrimSpace(string(bundle.Data)), "\n") + 1
	if m.RecordCount != lines {
		t.Errorf("manifest says %d records, the file holds %d", m.RecordCount, lines)
	}
}
