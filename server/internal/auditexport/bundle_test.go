package auditexport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func day(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func rows() []Record {
	return []Record{
		{ID: "b", ActorType: "member", Action: "workpaper_review_passed", At: day("2026-09-08").Add(9 * time.Hour)},
		{ID: "a", ActorType: "member", Action: "workpaper_submitted", At: day("2026-09-08").Add(8 * time.Hour)},
		{ID: "c", ActorType: "agent", Action: "workpaper_handed_over", At: day("2026-09-08").Add(10 * time.Hour)},
	}
}

// A re-run after a failure must produce the same bytes, or an operator cannot
// tell a retry from a tampered file.
func TestTheSameDayAlwaysProducesTheSameBytes(t *testing.T) {
	first := Build("ws-1", day("2026-09-08"), rows())
	shuffled := []Record{rows()[2], rows()[0], rows()[1]}
	second := Build("ws-1", day("2026-09-08"), shuffled)

	if string(first.Data) != string(second.Data) {
		t.Error("the same day's records produced different bytes depending on the order they arrived in")
	}
	if string(first.Manifest) != string(second.Manifest) {
		t.Error("the manifest is not deterministic")
	}
}

func TestRecordsAreOrderedOldestFirst(t *testing.T) {
	b := Build("ws-1", day("2026-09-08"), rows())
	lines := strings.Split(strings.TrimSpace(string(b.Data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	var ids []string
	for _, line := range lines {
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("line is not valid JSON: %v", err)
		}
		ids = append(ids, r.ID)
	}
	if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("order = %v, want a,b,c — a trail read out of order is not a trail", ids)
	}
}

// The manifest is what makes a truncated or missing export detectable. Without
// a count, a file with three of the day's four records looks exactly like a
// complete one.
func TestTheManifestCarriesTheCountAndTheDaysBoundaries(t *testing.T) {
	b := Build("ws-1", day("2026-09-08"), rows())
	var m Manifest
	if err := json.Unmarshal(b.Manifest, &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m.RecordCount != 3 {
		t.Errorf("record_count = %d, want 3", m.RecordCount)
	}
	if m.WorkspaceID != "ws-1" {
		t.Errorf("workspace_id = %q, want ws-1", m.WorkspaceID)
	}
	if m.From != "2026-09-08T00:00:00Z" || m.To != "2026-09-09T00:00:00Z" {
		t.Errorf("window = %s..%s, want the whole UTC day", m.From, m.To)
	}
}

// A day with nothing in it still gets a manifest. "No file" and "no activity"
// have to be distinguishable, or a failed export looks like a quiet day.
func TestAQuietDayStillProducesAManifest(t *testing.T) {
	b := Build("ws-1", day("2026-09-08"), nil)
	if len(b.Manifest) == 0 {
		t.Fatal("a quiet day produced no manifest; a missing export would be indistinguishable from no activity")
	}
	var m Manifest
	if err := json.Unmarshal(b.Manifest, &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m.RecordCount != 0 {
		t.Errorf("record_count = %d, want 0", m.RecordCount)
	}
	if len(b.Data) != 0 {
		t.Errorf("data = %q, want empty for a quiet day", b.Data)
	}
}

// Keys are what an operator globs and a lifecycle policy matches on, so their
// shape is part of the contract rather than an implementation detail.
func TestKeysArePartitionedByWorkspaceAndDay(t *testing.T) {
	b := Build("ws-1", day("2026-09-08"), rows())
	for _, key := range []string{b.DataKey, b.ManifestKey} {
		if !strings.HasPrefix(key, Prefix+"/") {
			t.Errorf("key %q is not under the audit export prefix %q", key, Prefix)
		}
		if !strings.Contains(key, "ws-1") || !strings.Contains(key, "2026-09-08") {
			t.Errorf("key %q does not name both the workspace and the day", key)
		}
	}
	if b.DataKey == b.ManifestKey {
		t.Error("the data and the manifest share a key; one would overwrite the other")
	}
}

// The count catches a truncated file. Only the digest catches an edited one —
// rewriting a record's level or actor leaves the count untouched.
func TestTheManifestDigestCoversTheRecordsFile(t *testing.T) {
	b := Build("ws-1", day("2026-09-08"), rows())
	var m Manifest
	if err := json.Unmarshal(b.Manifest, &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	sum := sha256.Sum256(b.Data)
	if m.DataSHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("digest = %q, does not match the records file", m.DataSHA256)
	}

	tampered := bytes.Replace(b.Data, []byte("workpaper_review_passed"), []byte("workpaper_filed________"), 1)
	if len(tampered) != len(b.Data) {
		t.Fatal("test setup changed the length; the point is an edit that preserves it")
	}
	tamperedSum := sha256.Sum256(tampered)
	if m.DataSHA256 == hex.EncodeToString(tamperedSum[:]) {
		t.Error("a same-length edit produced the same digest; the manifest cannot detect a rewritten record")
	}
}

func TestOneRecordPerLine(t *testing.T) {
	b := Build("ws-1", day("2026-09-08"), rows())
	if strings.Count(strings.TrimSpace(string(b.Data)), "\n") != 2 {
		t.Errorf("data is not one record per line:\n%s", b.Data)
	}
}
