package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auditdocs"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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
	URL          string `json:"url"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
	UploaderType string `json:"uploader_type"`
	UploaderID   string `json:"uploader_id"`
	CreatedAt    string `json:"created_at"`
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
	writeJSON(w, http.StatusCreated, AuditDocumentResponse{
		ID: uuidToString(row.ID), CategoryPath: row.CategoryPath, Title: row.Title,
		AttachmentID: uuidToString(row.AttachmentID),
		Filename:     att.Filename, URL: att.Url, ContentType: att.ContentType, SizeBytes: att.SizeBytes,
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
			Filename:     row.Filename, URL: row.Url,
			ContentType: row.ContentType, SizeBytes: row.SizeBytes,
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
func (h *Handler) DeleteAuditDocument(w http.ResponseWriter, r *http.Request) {
	member, workspaceID, ok := h.requireAuditeeMember(w, r)
	if !ok {
		return
	}
	if actorType, _ := h.resolveActor(r, uuidToString(member.UserID), workspaceID); actorType == "agent" {
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
	affected, err := h.Queries.DeleteAuditDocument(r.Context(), db.DeleteAuditDocumentParams{
		ID: idUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "document not found")
			return
		}
		slog.Warn("DeleteAuditDocument failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to delete the document")
		return
	}
	if affected == 0 {
		writeError(w, http.StatusNotFound, "document not found")
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
