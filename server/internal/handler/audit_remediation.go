package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// The 整改台账.
//
// A 整改事项 is a platform issue that belongs to no engagement — that is what
// makes it outlive the audit that found it. What audit needs beyond an issue is
// here: the department that owes the fix, the engagement that raised it, and
// the verification that closed it. The deadline is issue.due_date, which the
// platform already has on every list, filter and board; a second date column
// would be a second answer to "when is this due".
//
// Who may move an item is decided in internal/remediate and enforced on the
// ordinary issue write path, the same way the review chain is. Nothing in this
// file decides a transition.

const remediationPageLimit = 500

// AuditDepartmentResponse is one 责任部门.
type AuditDepartmentResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// OpenItems is what an interface needs to show the department's load
	// without a second round trip per row.
	ItemCount int64 `json:"item_count"`
}

// RemediationResponse is one row of the ledger.
type RemediationResponse struct {
	IssueID  string `json:"issue_id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	DueDate  string `json:"due_date,omitempty"`
	Overdue  bool   `json:"overdue"`
	DaysLate int    `json:"days_late,omitempty"`
	Assignee string `json:"assignee_id,omitempty"`

	DepartmentID   string `json:"department_id"`
	DepartmentName string `json:"department_name"`

	SourceProjectID    string `json:"source_project_id"`
	SourceProjectTitle string `json:"source_project_title"`
	SourceIssueID      string `json:"source_issue_id,omitempty"`

	VerifiedBy       string `json:"verified_by,omitempty"`
	VerifiedAt       string `json:"verified_at,omitempty"`
	VerificationNote string `json:"verification_note,omitempty"`

	CreatedAt string `json:"created_at"`
}

// ListAuditDepartments returns the auditee's department list with each one's
// load, in the order they were arranged.
func (h *Handler) ListAuditDepartments(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAuditeeMember(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListAuditDepartments(r.Context(), wsUUID)
	if err != nil {
		slog.Warn("ListAuditDepartments failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to read the department list")
		return
	}
	resp := make([]AuditDepartmentResponse, 0, len(rows))
	for _, row := range rows {
		count, countErr := h.Queries.CountRemediationInDepartment(r.Context(), db.CountRemediationInDepartmentParams{
			WorkspaceID: wsUUID, DepartmentID: row.ID,
		})
		if countErr != nil {
			slog.Warn("department item count failed", append(logger.RequestAttrs(r), "error", countErr)...)
		}
		resp = append(resp, AuditDepartmentResponse{
			ID:        uuidToString(row.ID),
			Name:      row.Name,
			ItemCount: count,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateAuditDepartmentRequest adds a department to the list.
type CreateAuditDepartmentRequest struct {
	Name string `json:"name"`
}

// CreateAuditDepartment adds one 责任部门.
func (h *Handler) CreateAuditDepartment(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	var req CreateAuditDepartmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "a department needs a name")
		return
	}
	if len([]rune(name)) > 64 {
		writeError(w, http.StatusBadRequest, "a department name is at most 64 characters")
		return
	}
	position, err := h.Queries.NextAuditDepartmentPosition(r.Context(), wsUUID)
	if err != nil {
		slog.Warn("department position failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to add the department")
		return
	}
	row, err := h.Queries.CreateAuditDepartment(r.Context(), db.CreateAuditDepartmentParams{
		WorkspaceID: wsUUID, Name: name, Position: float64(position),
	})
	if err != nil {
		if isUniqueViolation(err) {
			// The uniqueness is the point: 财务部 and 财务处 as two rows makes
			// every count by department quietly wrong.
			writeError(w, http.StatusConflict, "this auditee already has a department with that name")
			return
		}
		slog.Warn("CreateAuditDepartment failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to add the department")
		return
	}
	writeJSON(w, http.StatusCreated, AuditDepartmentResponse{ID: uuidToString(row.ID), Name: row.Name})
}

// DeleteAuditDepartment removes a department that owes nothing.
//
// A department with items on the ledger cannot be deleted, closed or open: a
// closed item still names the department that fixed it, and a report that
// cannot resolve the name has a hole in it.
func (h *Handler) DeleteAuditDepartment(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	deptUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "department id")
	if !ok {
		return
	}
	count, err := h.Queries.CountRemediationInDepartment(r.Context(), db.CountRemediationInDepartmentParams{
		WorkspaceID: wsUUID, DepartmentID: deptUUID,
	})
	if err != nil {
		slog.Warn("department item count failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to remove the department")
		return
	}
	if count > 0 {
		writeError(w, http.StatusConflict,
			"this department has remediation items on the ledger; reassign them before removing it")
		return
	}
	rows, err := h.Queries.DeleteAuditDepartment(r.Context(), db.DeleteAuditDepartmentParams{
		ID: deptUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		slog.Warn("DeleteAuditDepartment failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to remove the department")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "department not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RaiseRemediationRequest raises an item from the engagement that found the
// problem.
type RaiseRemediationRequest struct {
	Title        string  `json:"title"`
	Description  *string `json:"description"`
	DepartmentID string  `json:"department_id"`
	// DueDate is required: an item with no deadline is an item nobody chases.
	DueDate string `json:"due_date"`
	// AssigneeID is the 整改责任人, a member of the auditee.
	AssigneeID *string `json:"assignee_id"`
	// SourceIssueID is the workpaper the problem was found in, when it was
	// found in one.
	SourceIssueID *string `json:"source_issue_id"`
}

// RaiseRemediationItem creates a 整改事项 from an engagement.
//
// The item is created OUTSIDE the engagement — that is what makes it a
// remediation item rather than a workpaper, and what lets it outlive the audit.
// It keeps a pointer to the engagement that raised it, which is where the
// verifier's rank is read from when someone later says the problem is fixed.
func (h *Handler) RaiseRemediationItem(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin", "member")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	projectUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projectUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "engagement not found")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	// Raising an item is the audit function's act: it is an assertion that this
	// auditee owes a fix. A member with no rank on the engagement has not made
	// that finding.
	isAdmin := member.Role == "owner" || member.Role == "admin"
	if !isAdmin {
		level, levelErr := h.Queries.GetAuditRoleLevel(r.Context(), db.GetAuditRoleLevelParams{
			ProjectID: project.ID, MemberID: member.ID,
		})
		if levelErr != nil && !errors.Is(levelErr, pgx.ErrNoRows) {
			slog.Warn("audit role read failed", append(logger.RequestAttrs(r), "error", levelErr)...)
			writeError(w, http.StatusInternalServerError, "failed to raise the remediation item")
			return
		}
		if levelErr != nil || level == "" {
			writeError(w, http.StatusForbidden,
				"raising a remediation item needs a reviewer role on this engagement")
			return
		}
	}

	var req RaiseRemediationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "a remediation item needs a title")
		return
	}
	deptUUID, ok := parseUUIDOrBadRequest(w, req.DepartmentID, "department_id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetAuditDepartment(r.Context(), db.GetAuditDepartmentParams{
		ID: deptUUID, WorkspaceID: wsUUID,
	}); err != nil {
		// Refused rather than defaulted. An item filed against no department is
		// precisely the item nobody fixes, and picking one on the caller's
		// behalf would put the wrong name in next quarter's report.
		writeError(w, http.StatusBadRequest, "that department is not on this auditee's list")
		return
	}
	if strings.TrimSpace(req.DueDate) == "" {
		writeError(w, http.StatusBadRequest,
			"a remediation item needs a deadline; an item with no date is an item nobody chases")
		return
	}
	due, err := util.ParseCalendarDate(req.DueDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid due_date format, expected YYYY-MM-DD")
		return
	}

	var assignee pgtype.UUID
	assigneeType := pgtype.Text{}
	if req.AssigneeID != nil && strings.TrimSpace(*req.AssigneeID) != "" {
		assigneeUUID, parsed := parseUUIDOrBadRequest(w, *req.AssigneeID, "assignee_id")
		if !parsed {
			return
		}
		// A USER id, like every other assignee write on the platform
		// (validateAssigneePair). Storing a member id here would put a value in
		// issue.assignee_id that nothing else can resolve: the assignee renders
		// as "Unknown" everywhere, and — far worse — the ledger's
		// self-verification rule compares the assignee against the actor and
		// would never match, letting the person who owes the fix close their
		// own item.
		if _, memberErr := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
			UserID:      assigneeUUID,
			WorkspaceID: wsUUID,
		}); memberErr != nil {
			// A responsible person from another auditee is a deadline nobody
			// here owns.
			writeError(w, http.StatusBadRequest, "that member is not part of this auditee")
			return
		}
		assignee = assigneeUUID
		assigneeType = pgtype.Text{String: "member", Valid: true}
	}

	var sourceIssue pgtype.UUID
	if req.SourceIssueID != nil && strings.TrimSpace(*req.SourceIssueID) != "" {
		sourceUUID, parsed := parseUUIDOrBadRequest(w, *req.SourceIssueID, "source_issue_id")
		if !parsed {
			return
		}
		if _, issueErr := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
			ID: sourceUUID, WorkspaceID: wsUUID,
		}); issueErr != nil {
			writeError(w, http.StatusBadRequest, "that workpaper is not in this auditee")
			return
		}
		sourceIssue = sourceUUID
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	countPolicy := service.ResolveIssueCountPolicy(r.Context(), h.Entitlements, wsUUID)
	number, err := service.AllocateIssueNumber(r.Context(), qtx, wsUUID, countPolicy)
	if err != nil {
		if writeIssueLimitReached(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to allocate issue number")
		return
	}
	description := ""
	if req.Description != nil {
		description = *req.Description
	}
	issue, err := qtx.CreateIssue(r.Context(), db.CreateIssueParams{
		ID:           dbid.NewV7(),
		WorkspaceID:  wsUUID,
		Title:        title,
		Description:  strOrNullText(description),
		Status:       issuestatus.Todo,
		Priority:     "medium",
		AssigneeType: assigneeType,
		AssigneeID:   assignee,
		CreatorType:  "member",
		CreatorID:    parseUUID(userID),
		Position:     0,
		DueDate:      due,
		Number:       number,
		// No project. Engagement membership is what makes an issue a workpaper;
		// a remediation item belongs to none, and points at the engagement that
		// raised it through the ledger row below.
		ProjectID: pgtype.UUID{},
	})
	if err != nil {
		slog.Warn("raise remediation: create issue failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to raise the remediation item")
		return
	}
	record, err := qtx.CreateAuditRemediation(r.Context(), db.CreateAuditRemediationParams{
		IssueID:         issue.ID,
		WorkspaceID:     wsUUID,
		SourceProjectID: project.ID,
		SourceIssueID:   sourceIssue,
		DepartmentID:    deptUUID,
	})
	if err != nil {
		slog.Warn("raise remediation: create record failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to raise the remediation item")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to raise the remediation item")
		return
	}

	dept, _ := h.Queries.GetAuditDepartment(r.Context(), db.GetAuditDepartmentParams{ID: deptUUID, WorkspaceID: wsUUID})
	writeJSON(w, http.StatusCreated, remediationResponse(record, issue, dept.Name, project.Title))
}

// UpdateRemediationDepartmentRequest reassigns a mis-routed item.
type UpdateRemediationDepartmentRequest struct {
	DepartmentID string `json:"department_id"`
}

// UpdateRemediationDepartment moves an item to the department that actually
// owns the fix, without recreating it — the item keeps its history, its
// deadline and its place in the ledger.
func (h *Handler) UpdateRemediationDepartment(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	workspaceID := uuidToString(issue.WorkspaceID)
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin", "member")
	if !ok {
		return
	}
	record, err := h.Queries.GetAuditRemediation(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "this issue is not on the remediation ledger")
		return
	}
	isAdmin := member.Role == "owner" || member.Role == "admin"
	if !isAdmin {
		level, levelErr := h.Queries.GetAuditRoleLevel(r.Context(), db.GetAuditRoleLevelParams{
			ProjectID: record.SourceProjectID, MemberID: member.ID,
		})
		if levelErr != nil || level == "" {
			writeError(w, http.StatusForbidden,
				"reassigning a remediation item needs a reviewer role on the engagement that raised it")
			return
		}
	}
	var req UpdateRemediationDepartmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	deptUUID, ok := parseUUIDOrBadRequest(w, req.DepartmentID, "department_id")
	if !ok {
		return
	}
	dept, err := h.Queries.GetAuditDepartment(r.Context(), db.GetAuditDepartmentParams{
		ID: deptUUID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "that department is not on this auditee's list")
		return
	}
	updated, err := h.Queries.UpdateAuditRemediationDepartment(r.Context(), db.UpdateAuditRemediationDepartmentParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, DepartmentID: deptUUID,
	})
	if err != nil {
		slog.Warn("UpdateRemediationDepartment failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to reassign the remediation item")
		return
	}
	project, _ := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: record.SourceProjectID, WorkspaceID: issue.WorkspaceID,
	})
	writeJSON(w, http.StatusOK, remediationResponse(updated, issue, dept.Name, project.Title))
}

// ListRemediationLedger returns the 整改台账, filtered.
//
// Overdue is derived here, not stored: a stored flag is only as true as the
// last time a job ran, and the one question this ledger exists to answer is
// which items are late right now.
func (h *Handler) ListRemediationLedger(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAuditeeMember(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	params := db.ListAuditRemediationParams{
		WorkspaceID:  wsUUID,
		OverdueOnly:  r.URL.Query().Get("overdue") == "true",
		ClosedStatus: auditmode.StatusRemediationClosed,
		Lim:          remediationPageLimit,
	}
	if v := strings.TrimSpace(r.URL.Query().Get("department_id")); v != "" {
		deptUUID, parsed := parseUUIDOrBadRequest(w, v, "department_id")
		if !parsed {
			return
		}
		params.DepartmentID = deptUUID
	}
	if v := strings.TrimSpace(r.URL.Query().Get("source_project_id")); v != "" {
		projectUUID, parsed := parseUUIDOrBadRequest(w, v, "source_project_id")
		if !parsed {
			return
		}
		params.SourceProjectID = projectUUID
	}
	if v := strings.TrimSpace(r.URL.Query().Get("assignee_id")); v != "" {
		assigneeUUID, parsed := parseUUIDOrBadRequest(w, v, "assignee_id")
		if !parsed {
			return
		}
		params.AssigneeID = assigneeUUID
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		params.Status = pgtype.Text{String: v, Valid: true}
	}

	rows, err := h.Queries.ListAuditRemediation(r.Context(), params)
	if err != nil {
		slog.Warn("ListRemediationLedger failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to read the remediation ledger")
		return
	}
	resp := make([]RemediationResponse, 0, len(rows))
	for _, row := range rows {
		item := RemediationResponse{
			IssueID:            uuidToString(row.IssueID),
			Title:              row.Title,
			Status:             row.Status,
			DepartmentID:       uuidToString(row.DepartmentID),
			DepartmentName:     row.DepartmentName,
			SourceProjectID:    uuidToString(row.SourceProjectID),
			SourceProjectTitle: row.SourceProjectTitle,
			CreatedAt:          timestampToString(row.CreatedAt),
		}
		if row.SourceIssueID.Valid {
			item.SourceIssueID = uuidToString(row.SourceIssueID)
		}
		if row.AssigneeType.String == "member" && row.AssigneeID.Valid {
			item.Assignee = uuidToString(row.AssigneeID)
		}
		if row.DueDate.Valid {
			item.DueDate = row.DueDate.Time.Format("2006-01-02")
			item.Overdue, item.DaysLate = overdueBy(row.DueDate, row.Status, row.VerifiedAt)
		}
		if row.VerifiedAt.Valid {
			item.VerifiedBy = uuidToString(row.VerifiedBy)
			item.VerifiedAt = timestampToString(row.VerifiedAt)
			item.VerificationNote = row.VerificationNote.String
		}
		resp = append(resp, item)
	}
	writeJSON(w, http.StatusOK, resp)
}

// remediationResponse renders one ledger row.
func remediationResponse(record db.AuditRemediation, issue db.Issue, departmentName, projectTitle string) RemediationResponse {
	item := RemediationResponse{
		IssueID:            uuidToString(record.IssueID),
		Title:              issue.Title,
		Status:             issue.Status,
		DepartmentID:       uuidToString(record.DepartmentID),
		DepartmentName:     departmentName,
		SourceProjectID:    uuidToString(record.SourceProjectID),
		SourceProjectTitle: projectTitle,
		CreatedAt:          timestampToString(record.CreatedAt),
	}
	if record.SourceIssueID.Valid {
		item.SourceIssueID = uuidToString(record.SourceIssueID)
	}
	if issue.AssigneeType.String == "member" && issue.AssigneeID.Valid {
		item.Assignee = uuidToString(issue.AssigneeID)
	}
	if issue.DueDate.Valid {
		item.DueDate = issue.DueDate.Time.Format("2006-01-02")
		item.Overdue, item.DaysLate = overdueBy(issue.DueDate, issue.Status, record.VerifiedAt)
	}
	if record.VerifiedAt.Valid {
		item.VerifiedBy = uuidToString(record.VerifiedBy)
		item.VerifiedAt = timestampToString(record.VerifiedAt)
		item.VerificationNote = record.VerificationNote.String
	}
	return item
}

// overdueBy answers "is this late, and by how much" from the deadline and the
// state — never from a stored flag, which is only as true as the last job run.
// A closed or cancelled item is never late: it is finished, whenever it
// finished.
func overdueBy(due pgtype.Date, status string, verifiedAt pgtype.Timestamptz) (bool, int) {
	if !due.Valid || verifiedAt.Valid {
		return false, 0
	}
	if status == auditmode.StatusRemediationClosed || status == "cancelled" {
		return false, 0
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	deadline := due.Time.UTC().Truncate(24 * time.Hour)
	days := int(today.Sub(deadline).Hours() / 24)
	if days <= 0 {
		return false, 0
	}
	return true, days
}
