package auditarchive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The archive FORMAT, settled without a database. What needs one — which
// engagements may be archived and by whom — is in internal/handler.

func sample() Input {
	at := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	return Input{
		Engagement: Engagement{
			WorkspaceID: "ws-1", Auditee: "华东分公司", ProjectID: "p-1",
			Title: "2025 年离任审计", AuditType: "separation_of_office",
			PeriodStart: "2025-01-01", PeriodEnd: "2025-12-31",
			ReviewLevels: 2, Phase: "reporting",
		},
		Report: Report{
			ReportID: "r-1", Version: 1, Title: "2025 年度审计报告",
			IssuedBy: "审计部负责人", IssuedAt: at.Format(time.RFC3339),
			Opinion: "总体内控有效。",
		},
		ReportBody: "# 2025 年度审计报告\n",
		Workpapers: []Workpaper{
			{IssueID: "i-2", Number: 2, Title: "费用报销抽查", Status: "filed", UpdatedAt: at},
			{IssueID: "i-1", Number: 1, Title: "采购合同抽查", Status: "filed", UpdatedAt: at},
		},
		Trail: []TrailEntry{
			{ID: "b", IssueID: "i-1", Action: "workpaper_filed", At: at},
			{ID: "a", IssueID: "i-1", Action: "workpaper_submitted", At: at.Add(-time.Hour)},
		},
		Remediation: []RemediationItem{
			{IssueID: "i-9", Title: "三份合同未审批", Department: "采购部", Status: "remediating"},
		},
		Attachments: []Attachment{
			{AttachmentID: "a-1", IssueID: "i-1", Filename: "合同扫描件.pdf", SizeBytes: 1024},
		},
		ArchivedBy: "审计部负责人",
		ArchivedAt: at,
	}
}

func TestTheManifestListsEveryFileWithItsDigest(t *testing.T) {
	pkg := Build(sample())

	var m Manifest
	if err := json.Unmarshal(pkg.Manifest, &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(m.Files) != len(pkg.Files) {
		t.Fatalf("manifest lists %d files, archive wrote %d", len(m.Files), len(pkg.Files))
	}
	written := map[string][]byte{}
	for _, f := range pkg.Files {
		written[f.Key] = f.Data
	}
	for _, entry := range m.Files {
		data, ok := written[entry.Key]
		if !ok {
			t.Errorf("manifest names %q, which was not written", entry.Key)
			continue
		}
		digest := sha256.Sum256(data)
		if entry.SHA256 != hex.EncodeToString(digest[:]) {
			t.Errorf("%s: manifest digest does not match the file — the one thing the digest exists to catch", entry.Key)
		}
		if entry.Bytes != len(data) {
			t.Errorf("%s: manifest says %d bytes, file holds %d", entry.Key, entry.Bytes, len(data))
		}
	}
}

// A file holding three of four records is indistinguishable from a complete one
// without the count.
func TestTheManifestCountsWhatEachFileHolds(t *testing.T) {
	pkg := Build(sample())
	var m Manifest
	if err := json.Unmarshal(pkg.Manifest, &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	want := map[string]int{"workpapers.jsonl": 2, "trail.jsonl": 2, "remediation.jsonl": 1, "attachments.jsonl": 1}
	for _, entry := range m.Files {
		for suffix, count := range want {
			if strings.HasSuffix(entry.Key, suffix) && entry.Count != count {
				t.Errorf("%s: count = %d, want %d", entry.Key, entry.Count, count)
			}
		}
	}
}

// A retry after an interrupted run must not produce a different archive, or
// every digest becomes evidence of tampering that never happened.
func TestTwoRunsProduceIdenticalBytes(t *testing.T) {
	first, second := Build(sample()), Build(sample())
	if string(first.Manifest) != string(second.Manifest) {
		t.Error("two runs produced different manifests")
	}
	for i := range first.Files {
		if first.Files[i].Key != second.Files[i].Key || string(first.Files[i].Data) != string(second.Files[i].Data) {
			t.Errorf("file %d differs between runs", i)
		}
	}
}

// The order of what the handler happens to read must not reach the file.
func TestRecordOrderInTheInputDoesNotReachTheArchive(t *testing.T) {
	a := sample()
	b := sample()
	b.Workpapers[0], b.Workpapers[1] = b.Workpapers[1], b.Workpapers[0]
	b.Trail[0], b.Trail[1] = b.Trail[1], b.Trail[0]

	fileA, fileB := fileBySuffix(t, Build(a), "workpapers.jsonl"), fileBySuffix(t, Build(b), "workpapers.jsonl")
	if string(fileA) != string(fileB) {
		t.Error("the workpaper file depends on the order it was read in")
	}
	trailA, trailB := fileBySuffix(t, Build(a), "trail.jsonl"), fileBySuffix(t, Build(b), "trail.jsonl")
	if string(trailA) != string(trailB) {
		t.Error("the trail file depends on the order it was read in")
	}
}

// Oldest first: a trail read out of order is not a trail.
func TestTheTrailIsWrittenOldestFirst(t *testing.T) {
	data := string(fileBySuffix(t, Build(sample()), "trail.jsonl"))
	first := strings.Index(data, "workpaper_submitted")
	second := strings.Index(data, "workpaper_filed")
	if first < 0 || second < 0 || first > second {
		t.Errorf("trail is not in order:\n%s", data)
	}
}

func TestTheArchiveKeysLiveUnderOnePrefix(t *testing.T) {
	pkg := Build(sample())
	want := Prefix + "/ws-1/p-1/"
	for _, f := range pkg.Files {
		if !strings.HasPrefix(f.Key, want) {
			t.Errorf("file %q is outside the engagement's prefix", f.Key)
		}
	}
	if pkg.ManifestKey != ManifestKeyFor("ws-1", "p-1") {
		t.Errorf("manifest key = %q, want %q", pkg.ManifestKey, ManifestKeyFor("ws-1", "p-1"))
	}
}

// A reader treats a prefix with no manifest as unfinished, so the manifest is
// not one of the files: it is written after all of them.
func TestTheManifestIsNotOneOfTheFiles(t *testing.T) {
	pkg := Build(sample())
	for _, f := range pkg.Files {
		if f.Key == pkg.ManifestKey {
			t.Error("the manifest is in the file list; a crash mid-write could then leave it first")
		}
	}
}

func TestAnEmptyEngagementStillProducesACompleteArchive(t *testing.T) {
	in := sample()
	in.Workpapers, in.Trail, in.Remediation, in.Attachments = nil, nil, nil, nil
	pkg := Build(in)
	var m Manifest
	if err := json.Unmarshal(pkg.Manifest, &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(m.Files) != 6 {
		t.Errorf("an empty engagement wrote %d files, want the same 6 — a missing file is a gap, an empty one is a quiet audit", len(m.Files))
	}
}

func fileBySuffix(t *testing.T, pkg Package, suffix string) []byte {
	t.Helper()
	for _, f := range pkg.Files {
		if strings.HasSuffix(f.Key, suffix) {
			return f.Data
		}
	}
	t.Fatalf("no file ending in %q", suffix)
	return nil
}
