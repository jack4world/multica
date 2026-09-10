package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auditdocs"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// The auditee's document library.
//
// It belongs to the WORKSPACE, not to an engagement or an issue. A voucher
// arrives before anyone knows which workpaper will cite it, and once cited it is
// usually cited by several — filing it under one issue makes it invisible from
// the others and deletes it when that issue goes. The same client's material
// also spans every audit of it, which is what the auditee-is-a-workspace
// decision (ADR-0001) was for.
//
// The BYTES stay in a platform attachment, uploaded through the endpoint that
// already exists. This layer owns only what audit cares about: where a document
// is filed, what it is called, and who filed it.

const auditDocumentPageLimit = 500

// AuditCategoryResponse is one node of the filing scheme.
type AuditCategoryResponse struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// IsStandard marks the seeded scheme, so an interface can show which drawers
	// every auditee shares and which this client added.
	IsStandard bool   `json:"is_standard"`
	Depth      int    `json:"depth"`
	Parent     string `json:"parent,omitempty"`
}

// AuditDocumentResponse is one filed piece of material.
type AuditDocumentResponse struct {
	ID           string `json:"id"`
	CategoryPath string `json:"category_path"`
	Title        string `json:"title"`
	AttachmentID string `json:"attachment_id"`
	Filename     string `json:"filename"`
	// DownloadURL is a SHORT-LIVED SIGNED capability, not the storage URL.
	//
	// The raw storage URL used to be handed out here. On a local-storage
	// deployment /uploads/* is served on the root router, outside the auth
	// middleware, with no workspace check — so anyone holding that URL could
	// fetch an auditee's voucher, contract or bank statement with no session at
	// all. The platform accepts that trade-off for ordinary attachments (an
	// unguessable URL IS the credential, see MUL-5292); audit material does not
	// get to make that trade, because the URL travels through browser history,
	// referrers, proxy logs and corporate inspection appliances, and it never
	// expires.
	//
	// A capability minted per read expires in a minute. What is left is the
	// platform-level /uploads/* route itself, which is a product-wide decision
	// and deliberately not changed from inside this vertical.
	DownloadURL  string `json:"download_url"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
	UploaderType string `json:"uploader_type"`
	UploaderID   string `json:"uploader_id"`
	CreatedAt    string `json:"created_at"`
	// Set only on a withdrawn document. Absent everywhere else, because the
	// ordinary library listing never carries withdrawn material.
	WithdrawnAt      string `json:"withdrawn_at,omitempty"`
	WithdrawnBy      string `json:"withdrawn_by,omitempty"`
	WithdrawalReason string `json:"withdrawal_reason,omitempty"`
}

// ListWithdrawnAuditDocuments returns what the library used to hold.
//
// Withdrawal keeps the row and writes the trail, but without this read the
// library still could not answer "was anything taken out of here?" — the
// question the whole design exists to make answerable. Auditee membership, like
// every other read of the library: what was withdrawn, and why, is part of the
// file rather than a privileged view of it.
func (h *Handler) ListWithdrawnAuditDocuments(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAuditeeMember(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListWithdrawnAuditDocuments(r.Context(), db.ListWithdrawnAuditDocumentsParams{
		WorkspaceID: wsUUID,
		Lim:         auditDocumentPageLimit,
	})
	if err != nil {
		slog.Warn("ListWithdrawnAuditDocuments failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to read the withdrawn material")
		return
	}
	resp := make([]AuditDocumentResponse, 0, len(rows))
	for _, row := range rows {
		item := AuditDocumentResponse{
			ID: uuidToString(row.ID), CategoryPath: row.CategoryPath, Title: row.Title,
			AttachmentID: uuidToString(row.AttachmentID),
			Filename:     row.Filename,
			ContentType:  row.ContentType, SizeBytes: row.SizeBytes,
			UploaderType: row.UploaderType, UploaderID: uuidToString(row.UploaderID),
			CreatedAt:   timestampToString(row.CreatedAt),
			WithdrawnAt: timestampToString(row.WithdrawnAt),
			// No download capability: the material is out of the file. What
			// this view answers is that it WAS here and why it went, not a way
			// to keep reading it.
			WithdrawnBy:      uuidToString(row.WithdrawnBy),
			WithdrawalReason: row.WithdrawalReason.String,
		}
		resp = append(resp, item)
	}
	writeJSON(w, http.StatusOK, resp)
}

// requireAuditeeMember gates every library read and write on membership of the
// auditee, and nothing else. There is one isolation model (ADR-0001); a second
// one here would be a second answer to "who can see this".
func (h *Handler) requireAuditeeMember(w http.ResponseWriter, r *http.Request) (db.Member, string, bool) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin", "member")
	if !ok {
		return db.Member{}, "", false
	}
	return member, workspaceID, true
}

// ListAuditCategories returns the whole filing scheme in filing order.
func (h *Handler) ListAuditCategories(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAuditeeMember(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListAuditDocumentCategories(r.Context(), wsUUID)
	if err != nil {
		slog.Warn("ListAuditCategories failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to read the filing scheme")
		return
	}
	resp := make([]AuditCategoryResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, AuditCategoryResponse{
			Path:       row.Path,
			Name:       row.Name,
			IsStandard: row.IsStandard,
			Depth:      auditdocs.Depth(row.Path),
			Parent:     auditdocs.ParentPath(row.Path),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateAuditCategoryRequest adds a drawer this client needs.
type CreateAuditCategoryRequest struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// CreateAuditCategory extends the filing scheme.
func (h *Handler) CreateAuditCategory(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	var req CreateAuditCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	path := strings.TrimSpace(req.Path)
	if err := auditdocs.ValidatePath(path); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 64 {
		writeError(w, http.StatusBadRequest, "name must be 1-64 characters")
		return
	}
	// A drawer inside a cabinet that does not exist is a hole in the middle of
	// the tree, and it makes the parent's prefix read return something the
	// parent's own listing does not.
	if parent := auditdocs.ParentPath(path); parent != "" {
		if _, err := h.Queries.GetAuditDocumentCategory(r.Context(), db.GetAuditDocumentCategoryParams{
			WorkspaceID: wsUUID, Path: parent,
		}); err != nil {
			writeError(w, http.StatusBadRequest,
				"category "+parent+" does not exist; create it before "+path)
			return
		}
	}

	row, err := h.Queries.CreateAuditDocumentCategory(r.Context(), db.CreateAuditDocumentCategoryParams{
		WorkspaceID: wsUUID, Path: path, Name: name, Position: 0,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "category "+path+" already exists")
			return
		}
		slog.Warn("CreateAuditCategory failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to create the category")
		return
	}
	writeJSON(w, http.StatusCreated, AuditCategoryResponse{
		Path: row.Path, Name: row.Name, IsStandard: row.IsStandard,
		Depth: auditdocs.Depth(row.Path), Parent: auditdocs.ParentPath(row.Path),
	})
}

// DeleteAuditCategory removes an empty drawer.
//
// Refused while anything is under it — documents or sub-categories. Orphaning
// material by tidying the tree is the failure that would matter most here, and
// it is silent: the rows stay, pointing at a path nothing lists.
func (h *Handler) DeleteAuditCategory(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	path := chi.URLParam(r, "path")
	if err := auditdocs.ValidatePath(path); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	children, err := h.Queries.CountCategoryChildren(r.Context(), db.CountCategoryChildrenParams{
		WorkspaceID: wsUUID, DescendantPattern: auditdocs.DescendantPattern(path),
	})
	if err != nil {
		slog.Warn("DeleteAuditCategory children failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to delete the category")
		return
	}
	if children > 0 {
		writeError(w, http.StatusConflict,
			"this category has sub-categories; delete or move them first")
		return
	}
	docs, err := h.Queries.CountDocumentsUnderCategory(r.Context(), db.CountDocumentsUnderCategoryParams{
		WorkspaceID: wsUUID, Path: path, DescendantPattern: auditdocs.DescendantPattern(path),
	})
	if err != nil {
		slog.Warn("DeleteAuditCategory documents failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to delete the category")
		return
	}
	if docs > 0 {
		writeError(w, http.StatusConflict,
			"this category holds filed material; move it elsewhere before deleting the category")
		return
	}

	affected, err := h.Queries.DeleteAuditDocumentCategory(r.Context(), db.DeleteAuditDocumentCategoryParams{
		WorkspaceID: wsUUID, Path: path,
	})
	if err != nil {
		slog.Warn("DeleteAuditCategory failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to delete the category")
		return
	}
	if affected == 0 {
		writeError(w, http.StatusNotFound, "category not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// FileAuditDocumentRequest files an already-uploaded attachment.
type FileAuditDocumentRequest struct {
	AttachmentID string `json:"attachment_id"`
	CategoryPath string `json:"category_path"`
	Title        string `json:"title"`
}

// FileAuditDocument puts a piece of material in a drawer.
//
// The bytes are already in a platform attachment, uploaded through the endpoint
// that exists — this reuses the whole storage path rather than adding a second
// one for audit material.
func (h *Handler) FileAuditDocument(w http.ResponseWriter, r *http.Request) {
	member, workspaceID, ok := h.requireAuditeeMember(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	var req FileAuditDocumentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	attachmentUUID, ok := parseUUIDOrBadRequest(w, req.AttachmentID, "attachment_id")
	if !ok {
		return
	}
	path := strings.TrimSpace(req.CategoryPath)
	if err := auditdocs.ValidatePath(path); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" || len([]rune(title)) > 200 {
		writeError(w, http.StatusBadRequest, "title must be 1-200 characters")
		return
	}
	// Filing into a drawer that does not exist puts material where no browse
	// will find it — the same outcome as not filing it at all.
	if _, err := h.Queries.GetAuditDocumentCategory(r.Context(), db.GetAuditDocumentCategoryParams{
		WorkspaceID: wsUUID, Path: path,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "category "+path+" does not exist")
		return
	}
	// The attachment has to belong to THIS auditee: without the check, a
	// document could point at another workspace's bytes and the library would
	// serve them to everyone here.
	att, err := h.Queries.GetAttachment(r.Context(), db.GetAttachmentParams{
		ID: attachmentUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "attachment not found in this workspace")
		return
	}

	actorType, actorID := h.resolveActor(r, uuidToString(member.UserID), workspaceID)
	uploaderUUID, err := auditUploaderUUID(actorType, actorID, member)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not resolve the uploader")
		return
	}
	row, err := h.Queries.CreateAuditDocument(r.Context(), db.CreateAuditDocumentParams{
		WorkspaceID:  wsUUID,
		AttachmentID: attachmentUUID,
		CategoryPath: path,
		Title:        title,
		UploaderType: actorType,
		UploaderID:   uploaderUUID,
	})
	if err != nil {
		slog.Warn("FileAuditDocument failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to file the document")
		return
	}
	// Filing is recorded for the same reason withdrawal is: the trail is what
	// says the file grew, and a document that appears with no entry is one
	// nobody can date.
	if details, err := json.Marshal(map[string]any{
		"document_id":   uuidToString(row.ID),
		"attachment_id": uuidToString(row.AttachmentID),
		"category_path": row.CategoryPath,
		"title":         row.Title,
	}); err == nil {
		if _, err := h.Queries.CreateActivity(r.Context(), db.CreateActivityParams{
			ID:          dbid.NewV7(),
			WorkspaceID: wsUUID,
			ActorType:   pgtype.Text{String: row.UploaderType, Valid: row.UploaderType != ""},
			ActorID:     row.UploaderID,
			Action:      "audit_document_filed",
			Details:     details,
		}); err != nil {
			slog.Warn("filing trail failed", append(logger.RequestAttrs(r), "error", err)...)
		}
	}
	writeJSON(w, http.StatusCreated, AuditDocumentResponse{
		ID: uuidToString(row.ID), CategoryPath: row.CategoryPath, Title: row.Title,
		AttachmentID: uuidToString(row.AttachmentID),
		Filename:     att.Filename,
		DownloadURL:  attachmentDownloadCapabilityPath(uuidToString(row.AttachmentID), time.Now()),
		ContentType:  att.ContentType, SizeBytes: att.SizeBytes,
		UploaderType: row.UploaderType, UploaderID: uuidToString(row.UploaderID),
		CreatedAt: timestampToString(row.CreatedAt),
	})
}

// auditUploaderUUID resolves whichever id the actor type means: an agent's own
// id, or the acting member's user id.
func auditUploaderUUID(actorType, actorID string, member db.Member) (pgtype.UUID, error) {
	if actorType == "agent" {
		return util.ParseUUID(actorID)
	}
	return member.UserID, nil
}

// ListAuditDocuments returns everything filed under a category, at any depth.
//
// Asking for a parent returning only its own documents would mean walking the
// tree to find one voucher, which is the thing the path storage exists to
// avoid.
func (h *Handler) ListAuditDocuments(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAuditeeMember(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("category"))
	if err := auditdocs.ValidatePath(path); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := h.Queries.ListAuditDocumentsUnderCategory(r.Context(), db.ListAuditDocumentsUnderCategoryParams{
		WorkspaceID:       wsUUID,
		Path:              path,
		DescendantPattern: auditdocs.DescendantPattern(path),
		Lim:               auditDocumentPageLimit,
	})
	if err != nil {
		slog.Warn("ListAuditDocuments failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to read the library")
		return
	}
	resp := make([]AuditDocumentResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, AuditDocumentResponse{
			ID: uuidToString(row.ID), CategoryPath: row.CategoryPath, Title: row.Title,
			AttachmentID: uuidToString(row.AttachmentID),
			Filename:     row.Filename,
			DownloadURL:  attachmentDownloadCapabilityPath(uuidToString(row.AttachmentID), time.Now()),
			ContentType:  row.ContentType, SizeBytes: row.SizeBytes,
			UploaderType: row.UploaderType, UploaderID: uuidToString(row.UploaderID),
			CreatedAt: timestampToString(row.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// DeleteAuditDocument removes material from the audit file.
//
// People only. An agent may read the library and cite from it; material leaving
// the file is exactly as accountable as material entering it, and an agent
// cannot be held to that.
// WithdrawAuditDocumentRequest takes a document out of the library.
type WithdrawAuditDocumentRequest struct {
	// Reason is required. Audit practice is 撤下并注明原因; a withdrawal with no
	// reason is indistinguishable from material going missing.
	Reason string `json:"reason"`
}

// DeleteAuditDocument WITHDRAWS a document. It does not delete it.
//
// 审计资料 is the evidence the conclusions rest on. This used to be a hard
// DELETE any workspace member could perform, writing nothing anywhere — the one
// hole an append-only trail cannot cover, because what was never recorded needs
// no altering. A document removed before archival left a complete-looking
// 卷宗, a matching sha256 and a clean trail, and nothing at all to say it had
// existed.
//
// Three changes, and each closes a different half of that:
//   - the row survives, marked withdrawn, so the library can say what was here
//   - a trail entry records who took it down and why, in the append-only log
//     the daily export copies out of the database
//   - it takes owner/admin, the same bar as removing a drawer. The gradient was
//     inverted: changing a remediation item's status needed a rank and left a
//     trail, while destroying an original voucher needed neither.
func (h *Handler) DeleteAuditDocument(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, _ := h.resolveActor(r, userID, workspaceID)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents cannot remove material from the audit file")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	idUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "document id")
	if !ok {
		return
	}
	var req WithdrawAuditDocumentRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeErrorCode(w, http.StatusBadRequest, "reason_required",
			"say why this document is being withdrawn; material that leaves the file with no reason is indistinguishable from material going missing")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	doc, err := qtx.WithdrawAuditDocument(r.Context(), db.WithdrawAuditDocumentParams{
		ID:               idUUID,
		WorkspaceID:      wsUUID,
		WithdrawnBy:      member.UserID,
		WithdrawalReason: reason,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Either it does not exist here, or it is already withdrawn. Both
			// are "not in the library", and neither reveals more than that.
			writeError(w, http.StatusNotFound, "document not found")
			return
		}
		slog.Warn("WithdrawAuditDocument failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to withdraw the document")
		return
	}

	// In the SAME transaction: a withdrawal that committed without its record
	// would be exactly the silent removal this is here to end.
	details, err := json.Marshal(map[string]any{
		"document_id":   uuidToString(doc.ID),
		"attachment_id": uuidToString(doc.AttachmentID),
		"category_path": doc.CategoryPath,
		"title":         doc.Title,
		"reason":        reason,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record the withdrawal")
		return
	}
	if _, err := qtx.CreateActivity(r.Context(), db.CreateActivityParams{
		ID:          dbid.NewV7(),
		WorkspaceID: wsUUID,
		ActorType:   pgtype.Text{String: actorType, Valid: actorType != ""},
		ActorID:     parseUUID(userID),
		Action:      "audit_document_withdrawn",
		Details:     details,
	}); err != nil {
		slog.Warn("withdrawal trail failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to record the withdrawal")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to withdraw the document")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// seedAuditDocumentCategories installs the standard filing scheme.
//
// Idempotent, so the same code serves audit-mode enable and an auditee that
// predates the scheme. Parents come before children in the source list, which
// is what lets the create path check a parent exists without special-casing
// the seed.
func (h *Handler) seedAuditDocumentCategories(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID) error {
	for i, category := range auditdocs.StandardScheme() {
		name := category.Names["zh-Hans"]
		if name == "" {
			name = category.Names["en"]
		}
		if err := q.SeedAuditDocumentCategory(ctx, db.SeedAuditDocumentCategoryParams{
			WorkspaceID: workspaceID,
			Path:        category.Path,
			Name:        name,
			Position:    float64(i),
		}); err != nil {
			return err
		}
	}
	return nil
}
