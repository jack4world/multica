package storage

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// NewAuditExportStorageFromEnv returns the destination for the daily audit
// trail export, or nil when none is configured.
//
// IT DELIBERATELY DOES NOT FALL BACK to the attachment store. On a
// local-storage deployment the attachment store is served by GET /uploads/*,
// which is registered on the root router with NO authentication — anyone who
// knew a workspace UUID could fetch that workspace's entire day of activity,
// including someone already removed from it. On S3 the same objects would land
// in the attachment bucket behind the CDN with a public, five-day cache header.
// The audit trail is the most sensitive artifact this product writes; sharing a
// path with user attachments is not a configuration detail to get right later.
//
// So the destination is opt-in and separate: set AUDIT_EXPORT_DIR to a
// directory that is NOT inside LOCAL_UPLOAD_DIR. Unset means the export is off
// and the job is inert, which is the right default for every deployment not
// running the audit vertical.
func NewAuditExportStorageFromEnv() Storage {
	dir := strings.TrimSpace(os.Getenv("AUDIT_EXPORT_DIR"))
	if dir == "" {
		slog.Info("AUDIT_EXPORT_DIR not set, audit trail export disabled")
		return nil
	}

	uploadDir := os.Getenv("LOCAL_UPLOAD_DIR")
	if uploadDir == "" {
		uploadDir = "./data/uploads"
	}
	if within(dir, uploadDir) {
		// Refuse rather than warn. A warning in a startup log is not a control,
		// and the failure mode here is silent public exposure of the record the
		// whole vertical exists to protect.
		slog.Error("AUDIT_EXPORT_DIR is inside the publicly served upload directory; audit trail export disabled",
			"audit_export_dir", dir, "local_upload_dir", uploadDir)
		return nil
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Error("failed to create audit export directory", "dir", dir, "error", err)
		return nil
	}
	slog.Info("audit trail export storage initialized", "dir", dir)
	// No base URL: these objects are never linked to from the product, and an
	// empty one keeps ObjectURL from minting a fetchable address for them.
	return &LocalStorage{uploadDir: dir}
}

// within reports whether child resolves inside parent.
func within(child, parent string) bool {
	c, err := filepath.Abs(child)
	if err != nil {
		return true // cannot prove it is outside; refuse.
	}
	p, err := filepath.Abs(parent)
	if err != nil {
		return true
	}
	rel, err := filepath.Rel(p, c)
	if err != nil {
		return true
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}
