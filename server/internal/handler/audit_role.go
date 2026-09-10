package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Reviewer roles for one engagement.
//
// These endpoints ONLY feed the review gate. Nothing here may be read into a
// list, search, board, inbox or dispatch query: access isolation is auditee
// membership and nothing else (ADR-0001), and a role that also filters
// visibility would rebuild the cross-cutting access surface that ADR avoids.

// AuditRoleResponse is one reviewer assignment on an engagement.
type AuditRoleResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	MemberID  string `json:"member_id"`
	Level     string `json:"level"`
	CreatedAt string `json:"created_at"`
}

// SetAuditRoleRequest assigns one member one reviewer level.
type SetAuditRoleRequest struct {
	MemberID string `json:"member_id"`
	Level    string `json:"level"`
}

// levelWithinDepth reports whether a rank exists on an engagement of this
// depth.
func levelWithinDepth(level string, depth int) bool {
	for _, l := range auditgate.LevelsUpTo(depth) {
		if string(l) == level {
			return true
		}
	}
	return false
}

func auditRoleToResponse(row db.AuditRole) AuditRoleResponse {
	return AuditRoleResponse{
		ID:        uuidToString(row.ID),
		ProjectID: uuidToString(row.ProjectID),
		MemberID:  uuidToString(row.MemberID),
		Level:     row.Level,
		CreatedAt: timestampToString(row.CreatedAt),
	}
}

// loadEngagementForRoleAdmin resolves the engagement and proves the caller may
// change its reviewer ranks: a human owner or admin.
//
// Agents are refused before the role check, as they are for property
// definitions: an agent inherits its runtime owner's credentials, so without
// this an owner's agent could appoint itself a reviewer and then walk a
// workpaper through the chain it was built to gate.
func (h *Handler) loadEngagementForRoleAdmin(w http.ResponseWriter, r *http.Request) (db.Project, bool) {
	workspaceID := h.resolveWorkspaceID(r)
	idUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return db.Project{}, false
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.Project{}, false
	}
	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: idUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return db.Project{}, false
	}
	// Seating or removing a reviewer on a closed file changes who the archive
	// says was responsible, without changing the archive.
	if refuseArchivedEngagement(w, project) {
		return db.Project{}, false
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return db.Project{}, false
	}
	if actorType, _ := h.resolveActor(r, userID, workspaceID); actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents cannot manage audit reviewer roles")
		return db.Project{}, false
	}
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "project not found", "owner", "admin"); !ok {
		return db.Project{}, false
	}
	return project, true
}

// ListAuditRoles returns the reviewer ranks on one engagement. Readable by any
// member: a reviewer needs to know who to hand a workpaper to.
func (h *Handler) ListAuditRoles(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	idUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "project not found", "owner", "admin", "member"); !ok {
		return
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: idUUID, WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	rows, err := h.Queries.ListAuditRolesForProject(r.Context(), idUUID)
	if err != nil {
		slog.Warn("ListAuditRoles failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list audit roles")
		return
	}
	resp := make([]AuditRoleResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, auditRoleToResponse(db.AuditRole{
			ID: row.ID, WorkspaceID: row.WorkspaceID, ProjectID: row.ProjectID,
			MemberID: row.MemberID, Level: row.Level, CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy,
		}))
	}
	writeJSON(w, http.StatusOK, resp)
}

// SetAuditRole assigns one member one reviewer level on this engagement.
//
// Assigning a level to someone who already holds one REPLACES it. That is not a
// convenience: a person holds at most one level per engagement (enforced by a
// unique index), which is what makes "nobody reviews at two levels of the same
// workpaper" structural instead of a check the gate could forget.
func (h *Handler) SetAuditRole(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadEngagementForRoleAdmin(w, r)
	if !ok {
		return
	}
	var req SetAuditRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !auditgate.ValidLevel(req.Level) {
		writeError(w, http.StatusBadRequest, "level must be one of: reviewer_l1, reviewer_l2, reviewer_l3")
		return
	}
	// A 三级复核人 on a two-level engagement is a configuration error, and
	// catching it when it is made is far cheaper than catching it when a
	// workpaper cannot move and nobody knows why.
	if !levelWithinDepth(req.Level, int(project.ReviewLevels)) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"this engagement runs %d review levels, so %s is not one of its ranks",
			project.ReviewLevels, req.Level))
		return
	}
	memberUUID, ok := parseUUIDOrBadRequest(w, req.MemberID, "member id")
	if !ok {
		return
	}
	// The member has to belong to THIS auditee. Without the check an admin
	// could seat someone from another workspace as a reviewer, and the gate
	// would honour it.
	member, err := h.Queries.GetMember(r.Context(), memberUUID)
	if err != nil || member.WorkspaceID != project.WorkspaceID {
		writeError(w, http.StatusBadRequest, "member not found in this workspace")
		return
	}

	requester, ok := h.requireWorkspaceRole(w, r, uuidToString(project.WorkspaceID), "project not found", "owner", "admin")
	if !ok {
		return
	}
	row, err := h.Queries.SetAuditRole(r.Context(), db.SetAuditRoleParams{
		WorkspaceID: project.WorkspaceID,
		ProjectID:   project.ID,
		MemberID:    memberUUID,
		Level:       req.Level,
		CreatedBy:   pgtype.UUID{Bytes: requester.ID.Bytes, Valid: true},
	})
	if err != nil {
		slog.Warn("SetAuditRole failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to set audit role")
		return
	}
	writeJSON(w, http.StatusOK, auditRoleToResponse(row))
}

// DeleteAuditRole revokes a member's reviewer rank on this engagement.
//
// Revocation takes effect on the NEXT transition and never retroactively: a
// workpaper already past a level stays past it. Re-deciding history when
// staffing changes would make the chain unreadable.
func (h *Handler) DeleteAuditRole(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadEngagementForRoleAdmin(w, r)
	if !ok {
		return
	}
	memberUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "memberId"), "member id")
	if !ok {
		return
	}
	affected, err := h.Queries.DeleteAuditRole(r.Context(), db.DeleteAuditRoleParams{
		ProjectID: project.ID,
		MemberID:  memberUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "audit role not found")
			return
		}
		slog.Warn("DeleteAuditRole failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to delete audit role")
		return
	}
	if affected == 0 {
		writeError(w, http.StatusNotFound, "audit role not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
