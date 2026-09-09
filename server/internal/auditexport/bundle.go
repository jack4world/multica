// Package auditexport turns one auditee's activity for one day into the files
// that leave the database.
//
// The trail lives in activity_log, which workspace teardown deletes. That makes
// the export the DURABLE copy rather than a convenience: it is what lets the
// record of how a workpaper was approved outlive the workspace that produced
// it, including a mistaken deletion.
//
// Everything here is a pure function of the day's records, so the format — the
// part an operator, an auditor, or a future importer depends on — is settled
// without a database or a storage backend.
package auditexport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Prefix is the root every exported object lives under, so a bucket policy,
// a lifecycle rule or an operator's glob can address the audit exports alone.
const Prefix = "audit-trail"

// Record is one trail entry as exported. It is a projection of activity_log
// rather than the row itself: the export is a published format, and pinning it
// here means a later column addition is a deliberate change to what auditors
// receive rather than an accident.
type Record struct {
	ID        string          `json:"id"`
	IssueID   string          `json:"issue_id,omitempty"`
	ActorType string          `json:"actor_type,omitempty"`
	ActorID   string          `json:"actor_id,omitempty"`
	Action    string          `json:"action"`
	Details   json.RawMessage `json:"details,omitempty"`
	At        time.Time       `json:"at"`
}

// Manifest accompanies every day, including an empty one.
//
// The count is what makes a truncated export detectable: a file holding three
// of the day's four records is indistinguishable from a complete one without
// it. The window is what makes a MISSING export detectable — a day with no
// manifest is a gap, where a day with a zero-count manifest is a quiet day.
type Manifest struct {
	WorkspaceID string `json:"workspace_id"`
	Day         string `json:"day"`
	From        string `json:"from"`
	To          string `json:"to"`
	RecordCount int    `json:"record_count"`
	DataKey     string `json:"data_key"`
	Format      string `json:"format"`
	// DataSHA256 is what makes an EDIT detectable, as opposed to a truncation.
	// A count catches a file that lost records; it says nothing about one whose
	// records were rewritten in place — changing an entry's level or actor
	// preserves the count exactly. The manifest is a published format, so the
	// digest has to be here from the first release rather than added later.
	DataSHA256 string `json:"data_sha256"`
}

// Bundle is what one workspace-day writes to storage.
type Bundle struct {
	DataKey     string
	Data        []byte
	ManifestKey string
	Manifest    []byte
}

// Build renders one workspace-day. Deterministic: the same records in any order
// produce byte-identical output, so re-running a day after a failure is safe,
// and the manifest's digest is what lets an operator tell a retry from a
// tampered file.
func Build(workspaceID string, day time.Time, records []Record) Bundle {
	day = day.UTC().Truncate(24 * time.Hour)
	dayKey := day.Format("2006-01-02")

	ordered := make([]Record, len(records))
	copy(ordered, records)
	// Oldest first, with the id breaking ties: a trail read out of order is not
	// a trail, and two entries can share a timestamp.
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].At.Equal(ordered[j].At) {
			return ordered[i].At.Before(ordered[j].At)
		}
		return ordered[i].ID < ordered[j].ID
	})

	var data bytes.Buffer
	for _, r := range ordered {
		// One record per line, so a reader can stream the file and a diff
		// between two exports of the same day points at a single record.
		line, err := json.Marshal(r)
		if err != nil {
			// A record that cannot be encoded is a bug in the projection, not
			// an operational condition; recording the failure inline keeps the
			// export honest rather than silently short.
			line = []byte(fmt.Sprintf(`{"id":%q,"error":"unencodable record"}`, r.ID))
		}
		data.Write(line)
		data.WriteByte('\n')
	}

	dataKey := fmt.Sprintf("%s/%s/%s/records.jsonl", Prefix, workspaceID, dayKey)
	digest := sha256.Sum256(data.Bytes())
	manifest := Manifest{
		WorkspaceID: workspaceID,
		Day:         dayKey,
		From:        day.Format(time.RFC3339),
		To:          day.Add(24 * time.Hour).Format(time.RFC3339),
		RecordCount: len(ordered),
		DataKey:     dataKey,
		Format:      "jsonl",
		DataSHA256:  hex.EncodeToString(digest[:]),
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		encoded = []byte(`{"error":"unencodable manifest"}`)
	}

	return Bundle{
		DataKey:     dataKey,
		Data:        data.Bytes(),
		ManifestKey: fmt.Sprintf("%s/%s/%s/manifest.json", Prefix, workspaceID, dayKey),
		Manifest:    encoded,
	}
}
