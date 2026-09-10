// Package auditarchive turns one finished engagement into the files that make
// up its 卷宗.
//
// An audit ends with an archive: the report, the workpapers that support it,
// the evidence behind those, and an index, kept for as long as the records
// policy says. The daily trail export (internal/auditexport) answers a
// different question — it is what makes the trail outlive the database — and
// this does not replace it: an auditee's trail includes work that belongs to no
// engagement, and every remediation item is exactly that.
//
// Everything here is a pure function of values, for the reason auditexport is:
// the format is what an operator, an auditor and any future importer depend on,
// so it is settled where a test can pin it.
package auditarchive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Prefix is the root every archived engagement lives under, so one bucket
// policy or one operator's glob addresses the archives alone.
const Prefix = "audit-archive"

// Engagement is what the manifest says about the audit being archived.
type Engagement struct {
	WorkspaceID  string `json:"workspace_id"`
	Auditee      string `json:"auditee"`
	ProjectID    string `json:"project_id"`
	Title        string `json:"title"`
	AuditType    string `json:"audit_type,omitempty"`
	PeriodStart  string `json:"period_start,omitempty"`
	PeriodEnd    string `json:"period_end,omitempty"`
	ReviewLevels int    `json:"review_levels"`
	Phase        string `json:"phase,omitempty"`
}

// Workpaper is one workpaper as archived.
type Workpaper struct {
	IssueID string `json:"issue_id"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	// Properties are the workpaper's audit fields — procedure code, procedure
	// type, conclusion — by NAME, not by definition id. An id means nothing to
	// someone opening the archive in three years.
	Properties map[string]string `json:"properties,omitempty"`
	// Preparer is the person, resolved. An archive is a snapshot, not a set of
	// foreign keys: the reader has no database to join against, and the person
	// may have left the company before anyone opens the file.
	Preparer Person `json:"preparer,omitempty"`
	// PreparerID is kept as a technical reference for anyone re-importing into
	// a live system. It is a USER id, the same namespace as TrailEntry.Actor —
	// the first version of this file wrote a member id here and a user id
	// there, so the two could not be matched at all.
	PreparerID  string    `json:"preparer_id,omitempty"`
	SubmittedAt string    `json:"submitted_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Person is who someone WAS, at the moment the file was closed.
//
// The whole reason an archive exists is that it can be read without the system
// that produced it. A bare uuid in a trail entry answers "who approved this?"
// with "look it up" — against a database the reader does not have, holding a
// person who may have left, in a company that may have replaced the software.
// So the archive stores the name and the standing, and keeps the id only as a
// technical reference.
type Person struct {
	ID string `json:"id,omitempty"`
	// Name as it stood at archival. Empty when the id no longer resolves,
	// which is itself worth recording: it says the person was already gone.
	Name string `json:"name,omitempty"`
	// Role on this engagement at archival — 主审 / 项目经理 / 部门负责人 — or
	// empty for someone who held no rank on it.
	Level string `json:"level,omitempty"`
}

// TrailEntry is one step of the engagement's history.
type TrailEntry struct {
	ID        string `json:"id"`
	IssueID   string `json:"issue_id,omitempty"`
	ActorType string `json:"actor_type,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	// Actor is ActorID resolved. See Person: an archive nobody can read the
	// names in is an archive that answers nothing.
	Actor   Person          `json:"actor,omitempty"`
	Action  string          `json:"action"`
	Details json.RawMessage `json:"details,omitempty"`
	At      time.Time       `json:"at"`
}

// RemediationItem is one item this engagement raised, as it stood at archival.
type RemediationItem struct {
	IssueID    string `json:"issue_id"`
	Title      string `json:"title"`
	Department string `json:"department"`
	DueDate    string `json:"due_date,omitempty"`
	Status     string `json:"status"`
	VerifiedAt string `json:"verified_at,omitempty"`
	// Responsible is who owed the fix, and Verifier who signed it off, both as
	// people rather than ids.
	Responsible Person `json:"responsible,omitempty"`
	Verifier    Person `json:"verifier,omitempty"`
}

// Attachment is one piece of evidence, indexed rather than copied. The bytes
// live in the attachment store; what the archive owns is the fact that this
// file, of this size, hung on that workpaper — which is what makes a later
// disappearance detectable.
type Attachment struct {
	AttachmentID string `json:"attachment_id"`
	IssueID      string `json:"issue_id"`
	Filename     string `json:"filename"`
	ContentType  string `json:"content_type,omitempty"`
	SizeBytes    int64  `json:"size_bytes"`
}

// Report is the issued report, structured beside its rendering.
type Report struct {
	ReportID     string `json:"report_id"`
	Version      int    `json:"version"`
	Title        string `json:"title"`
	IssuedBy     string `json:"issued_by,omitempty"`
	IssuedAt     string `json:"issued_at,omitempty"`
	Background   string `json:"background,omitempty"`
	Basis        string `json:"basis,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Opinion      string `json:"opinion,omitempty"`
	Requirements string `json:"requirements,omitempty"`
}

// Input is everything one archive is built from.
type Input struct {
	Engagement  Engagement
	Report      Report
	ReportBody  string
	Workpapers  []Workpaper
	Trail       []TrailEntry
	Remediation []RemediationItem
	Attachments []Attachment
	ArchivedBy  string
	ArchivedAt  time.Time
}

// FileEntry is one archived file, as the manifest lists it.
type FileEntry struct {
	Key    string `json:"key"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
	// Count is how many records the file holds, for the line-oriented ones. A
	// file holding three of four records is indistinguishable from a complete
	// one without it.
	Count int `json:"count,omitempty"`
}

// Manifest is the index of the file, and the thing that makes tampering
// detectable. A prefix with no manifest is an unfinished archive.
type Manifest struct {
	Engagement Engagement  `json:"engagement"`
	ArchivedBy string      `json:"archived_by,omitempty"`
	ArchivedAt string      `json:"archived_at"`
	Report     Report      `json:"report"`
	Files      []FileEntry `json:"files"`
	Format     string      `json:"format"`
}

// File is one object to write.
type File struct {
	Key  string
	Data []byte
}

// Package is what one engagement writes to storage. Files are in write order
// and the manifest is LAST: a reader treats a prefix with no manifest as
// unfinished, so writing it first would let a crash present a truncated archive
// as complete.
type Package struct {
	Files       []File
	ManifestKey string
	Manifest    []byte
}

// Build renders one engagement's archive.
//
// Deterministic: the same input twice produces byte-identical output, so a
// retry after an interrupted run is safe, and a digest that no longer matches
// its file is evidence of an edit rather than of a retry.
func Build(in Input) Package {
	root := fmt.Sprintf("%s/%s/%s", Prefix, in.Engagement.WorkspaceID, in.Engagement.ProjectID)

	workpapers := append([]Workpaper(nil), in.Workpapers...)
	sort.SliceStable(workpapers, func(i, j int) bool { return workpapers[i].Number < workpapers[j].Number })

	trail := append([]TrailEntry(nil), in.Trail...)
	// Oldest first, id breaking ties: a trail read out of order is not a trail,
	// and two entries can share a timestamp.
	sort.SliceStable(trail, func(i, j int) bool {
		if !trail[i].At.Equal(trail[j].At) {
			return trail[i].At.Before(trail[j].At)
		}
		return trail[i].ID < trail[j].ID
	})

	remediation := append([]RemediationItem(nil), in.Remediation...)
	sort.SliceStable(remediation, func(i, j int) bool { return remediation[i].IssueID < remediation[j].IssueID })

	attachments := append([]Attachment(nil), in.Attachments...)
	sort.SliceStable(attachments, func(i, j int) bool {
		return attachments[i].AttachmentID < attachments[j].AttachmentID
	})

	files := []File{
		{Key: root + "/report.md", Data: []byte(in.ReportBody)},
		{Key: root + "/report.json", Data: encode(in.Report)},
		{Key: root + "/workpapers.jsonl", Data: lines(workpapers)},
		{Key: root + "/trail.jsonl", Data: lines(trail)},
		{Key: root + "/remediation.jsonl", Data: lines(remediation)},
		{Key: root + "/attachments.jsonl", Data: lines(attachments)},
	}
	counts := map[string]int{
		root + "/workpapers.jsonl":  len(workpapers),
		root + "/trail.jsonl":       len(trail),
		root + "/remediation.jsonl": len(remediation),
		root + "/attachments.jsonl": len(attachments),
	}

	entries := make([]FileEntry, 0, len(files))
	for _, f := range files {
		digest := sha256.Sum256(f.Data)
		entries = append(entries, FileEntry{
			Key:    f.Key,
			Bytes:  len(f.Data),
			SHA256: hex.EncodeToString(digest[:]),
			Count:  counts[f.Key],
		})
	}

	manifest := Manifest{
		Engagement: in.Engagement,
		ArchivedBy: in.ArchivedBy,
		ArchivedAt: in.ArchivedAt.UTC().Format(time.RFC3339),
		Report:     in.Report,
		Files:      entries,
		Format:     "jsonl",
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		encoded = []byte(`{"error":"unencodable manifest"}`)
	}

	return Package{
		Files:       files,
		ManifestKey: root + "/manifest.json",
		Manifest:    encoded,
	}
}

// ManifestKeyFor is where an engagement's manifest lives, for callers that need
// the address before the archive exists.
func ManifestKeyFor(workspaceID, projectID string) string {
	return fmt.Sprintf("%s/%s/%s/manifest.json", Prefix, workspaceID, projectID)
}

func encode(v any) []byte {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return []byte(`{"error":"unencodable"}`)
	}
	return out
}

// lines writes one record per line, so a reader can stream the file and a diff
// between two archives points at a single record.
func lines[T any](records []T) []byte {
	var b bytes.Buffer
	for _, r := range records {
		line, err := json.Marshal(r)
		if err != nil {
			// A record that cannot be encoded is a bug in the projection, not
			// an operational condition; recording it inline keeps the archive
			// honest rather than silently short.
			line = []byte(`{"error":"unencodable record"}`)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.Bytes()
}
