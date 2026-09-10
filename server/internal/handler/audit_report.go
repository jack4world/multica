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
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/auditreport"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// 审计报告: the engagement's deliverable, and the only thing anyone outside the
// audit function reads.
//
// The chain lives in internal/auditreport and is enforced here. What this file
// owns is the snapshot: at the moment of signing, the report stores the items
// it cited and how much work stood behind it, so a report issued in January
// still says in June what it said when it went out.

// AuditReportResponse is one report.
type AuditReportResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Version   int    `json:"version"`
	Status    string `json:"status"`
	Title     string `json:"title"`

	Background   string `json:"background"`
	Basis        string `json:"basis"`
	Scope        string `json:"scope"`
	Opinion      string `json:"opinion"`
	Requirements string `json:"requirements"`

	// Findings are live while the report is a draft and the snapshot once it is
	// issued — which is the same sentence as "an issued report does not change".
	Findings        []auditreport.Finding `json:"findings"`
	WorkpaperCount  int                   `json:"workpaper_count"`
	FiledWorkpapers int                   `json:"filed_workpaper_count"`

	IssuedBy  string `json:"issued_by,omitempty"`
	IssuedAt  string `json:"issued_at,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// CreateAuditReportRequest starts a report for an engagement.
type CreateAuditReportRequest struct {
	Title string `json:"title"`
}

// CreateAuditReport starts the engagement's next report.
func (h *Handler) CreateAuditReport(w http.ResponseWriter, r *http.Request) {
	project, _, ok := h.requireEngagementRank(w, r)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req CreateAuditReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = project.Title + " 审计报告"
	}
	if len([]rune(title)) > 200 {
		writeError(w, http.StatusBadRequest, "a report title is at most 200 characters")
		return
	}

	version, err := h.Queries.NextAuditReportVersion(r.Context(), project.ID)
	if err != nil {
		slog.Warn("NextAuditReportVersion failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to start the report")
		return
	}
	report, err := h.Queries.CreateAuditReport(r.Context(), db.CreateAuditReportParams{
		WorkspaceID: project.WorkspaceID,
		ProjectID:   project.ID,
		Version:     version,
		Title:       title,
		CreatedBy:   parseUUID(userID),
	})
	if err != nil {
		if isUniqueViolation(err) {
			// Two people drafting two reports nobody reconciles is how an audit
			// issues the wrong one.
			writeError(w, http.StatusConflict,
				"this engagement already has an unsigned report; issue or finish that one first")
			return
		}
		slog.Warn("CreateAuditReport failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to start the report")
		return
	}
	resp, err := h.auditReportResponse(r.Context(), report)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read the report")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// ListAuditReports returns an engagement's reports, newest version first.
func (h *Handler) ListAuditReports(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAuditeeMember(w, r)
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
	rows, err := h.Queries.ListAuditReportsForProject(r.Context(), db.ListAuditReportsForProjectParams{
		ProjectID: projectUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		slog.Warn("ListAuditReports failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to read the engagement's reports")
		return
	}
	resp := make([]AuditReportResponse, 0, len(rows))
	for _, row := range rows {
		item, err := h.auditReportResponse(r.Context(), row)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read the engagement's reports")
			return
		}
		resp = append(resp, item)
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetAuditReport returns one report.
func (h *Handler) GetAuditReport(w http.ResponseWriter, r *http.Request) {
	report, _, ok := h.loadAuditReport(w, r)
	if !ok {
		return
	}
	resp, err := h.auditReportResponse(r.Context(), report)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read the report")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// UpdateAuditReportRequest edits an unsigned report. An absent key leaves that
// section untouched; the sections are a closed list because an internal audit
// report's structure is not user-configurable.
type UpdateAuditReportRequest struct {
	Title        *string `json:"title"`
	Background   *string `json:"background"`
	Basis        *string `json:"basis"`
	Scope        *string `json:"scope"`
	Opinion      *string `json:"opinion"`
	Requirements *string `json:"requirements"`
	// Status moves the report through its chain. Sent on its own or alongside
	// text; the chain is decided either way.
	Status *string `json:"status"`
	// Reason accompanies a return to the drafter.
	Reason *string `json:"reason"`
}

// UpdateAuditReport writes an unsigned report, and moves it through its chain.
//
// One endpoint for both because they are one write: a reviewer who fixes a
// sentence and signs in the same breath must not be able to produce a signed
// report whose text was written after the signature.
func (h *Handler) UpdateAuditReport(w http.ResponseWriter, r *http.Request) {
	report, member, ok := h.loadAuditReport(w, r)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req UpdateAuditReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	target := strings.TrimSpace(strPtrValue(req.Status))
	reason := strPtrValue(req.Reason)

	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: report.ProjectID, WorkspaceID: report.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "engagement not found")
		return
	}

	actorType, _ := h.resolveActor(r, userID, uuidToString(report.WorkspaceID))
	in := auditreport.Input{
		From:         report.Status,
		To:           target,
		ActorIsAdmin: member.Role == "owner" || member.Role == "admin",
		ActorIsAgent: actorType == "agent",
		ReviewLevels: int(project.ReviewLevels),
		Reason:       reason,
	}
	if !in.ActorIsAgent {
		level, levelErr := h.Queries.GetAuditRoleLevel(r.Context(), db.GetAuditRoleLevelParams{
			ProjectID: project.ID, MemberID: member.ID,
		})
		if levelErr != nil && !errors.Is(levelErr, pgx.ErrNoRows) {
			slog.Warn("audit role read failed", append(logger.RequestAttrs(r), "error", levelErr)...)
			writeError(w, http.StatusInternalServerError, "failed to write the report")
			return
		}
		if levelErr == nil {
			in.ActorLevel = auditgate.Level(level)
		}
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// Decided on the row as it stands inside this transaction. From the
	// handler's snapshot, "an issued report cannot be changed" would only be as
	// true as the absence of a concurrent signature.
	locked, err := qtx.LockAuditReport(r.Context(), db.LockAuditReportParams{
		ID: report.ID, WorkspaceID: report.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "report not found")
		return
	}
	in.From = locked.Status

	decision := auditreport.Decide(in)
	if !decision.Allowed {
		status := http.StatusConflict
		switch decision.Code {
		case auditreport.DenyRankRequired, auditreport.DenyAgent:
			status = http.StatusForbidden
		case auditreport.DenyReasonRequired:
			status = http.StatusBadRequest
		}
		writeErrorCode(w, status, string(decision.Code), decision.Reason)
		return
	}

	updated := locked
	if req.Title != nil || req.Background != nil || req.Basis != nil ||
		req.Scope != nil || req.Opinion != nil || req.Requirements != nil {
		if req.Title != nil && len([]rune(strings.TrimSpace(*req.Title))) > 200 {
			writeError(w, http.StatusBadRequest, "a report title is at most 200 characters")
			return
		}
		updated, err = qtx.UpdateAuditReportSections(r.Context(), db.UpdateAuditReportSectionsParams{
			ID:           report.ID,
			WorkspaceID:  report.WorkspaceID,
			Title:        textOrNull(req.Title),
			Background:   textOrNull(req.Background),
			Basis:        textOrNull(req.Basis),
			Scope:        textOrNull(req.Scope),
			Opinion:      textOrNull(req.Opinion),
			Requirements: textOrNull(req.Requirements),
		})
		if err != nil {
			slog.Warn("UpdateAuditReportSections failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to write the report")
			return
		}
	}

	switch {
	case decision.Event == auditreport.EventIssued:
		snapshot, workpapers, filed, snapErr := h.snapshotForReport(r.Context(), qtx, project.ID)
		if snapErr != nil {
			slog.Warn("report snapshot failed", append(logger.RequestAttrs(r), "error", snapErr)...)
			writeError(w, http.StatusInternalServerError, "failed to issue the report")
			return
		}
		encoded, marshalErr := json.Marshal(snapshot)
		if marshalErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to issue the report")
			return
		}
		updated, err = qtx.IssueAuditReport(r.Context(), db.IssueAuditReportParams{
			ID:                  report.ID,
			WorkspaceID:         report.WorkspaceID,
			IssuedBy:            member.ID,
			FindingsSnapshot:    encoded,
			WorkpaperCount:      int32(workpapers),
			FiledWorkpaperCount: int32(filed),
		})
		if err != nil {
			slog.Warn("IssueAuditReport failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to issue the report")
			return
		}
	case target != "" && target != in.From:
		updated, err = qtx.SetAuditReportStatus(r.Context(), db.SetAuditReportStatusParams{
			ID: report.ID, WorkspaceID: report.WorkspaceID, Status: target,
		})
		if err != nil {
			slog.Warn("SetAuditReportStatus failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to write the report")
			return
		}
	}

	// In the SAME transaction as the change it describes, for the reason the
	// review gate's trail is: a report that goes out with nothing saying who
	// signed it is a hole in exactly the record this exists to make.
	if decision.Event != "" {
		if err := h.recordReportTrail(r.Context(), qtx, updated, decision, actorType, userID, reason); err != nil {
			slog.Warn("report trail failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to record the report decision")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write the report")
		return
	}

	resp, err := h.auditReportResponse(r.Context(), updated)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read the report")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ExportAuditReport renders the report as Markdown or HTML.
//
// Two formats, no Word and no PDF: those are a rendering chain and a template
// negotiation with each organization, and neither changes what the report says.
func (h *Handler) ExportAuditReport(w http.ResponseWriter, r *http.Request) {
	report, _, ok := h.loadAuditReport(w, r)
	if !ok {
		return
	}
	doc, err := h.reportDocument(r.Context(), report)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to render the report")
		return
	}
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format"))) {
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(auditreport.HTML(doc)))
	default:
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(auditreport.Markdown(doc)))
	}
}

// requireEngagementRank gates the acts that assert something on the audit
// function's behalf. Workspace admin counts: creating the report is
// administrative, signing it is not (see internal/auditreport).
func (h *Handler) requireEngagementRank(w http.ResponseWriter, r *http.Request) (db.Project, db.Member, bool) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin", "member")
	if !ok {
		return db.Project{}, db.Member{}, false
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.Project{}, db.Member{}, false
	}
	projectUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return db.Project{}, db.Member{}, false
	}
	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projectUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "engagement not found")
		return db.Project{}, db.Member{}, false
	}
	if member.Role == "owner" || member.Role == "admin" {
		return project, member, true
	}
	level, err := h.Queries.GetAuditRoleLevel(r.Context(), db.GetAuditRoleLevelParams{
		ProjectID: project.ID, MemberID: member.ID,
	})
	if err != nil || level == "" {
		writeError(w, http.StatusForbidden, "this needs a reviewer role on this engagement")
		return db.Project{}, db.Member{}, false
	}
	return project, member, true
}

// loadAuditReport resolves the report in the path and the acting member.
func (h *Handler) loadAuditReport(w http.ResponseWriter, r *http.Request) (db.AuditReport, db.Member, bool) {
	member, workspaceID, ok := h.requireAuditeeMember(w, r)
	if !ok {
		return db.AuditReport{}, db.Member{}, false
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.AuditReport{}, db.Member{}, false
	}
	reportUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "reportId"), "report id")
	if !ok {
		return db.AuditReport{}, db.Member{}, false
	}
	report, err := h.Queries.GetAuditReport(r.Context(), db.GetAuditReportParams{
		ID: reportUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "report not found")
		return db.AuditReport{}, db.Member{}, false
	}
	return report, member, true
}

// snapshotForReport reads what the report is about to assert: the items this
// engagement raised, as they stand, and how much work is behind it.
func (h *Handler) snapshotForReport(ctx context.Context, q *db.Queries, projectID pgtype.UUID) ([]auditreport.Finding, int, int, error) {
	rows, err := q.ListRemediationForReport(ctx, projectID)
	if err != nil {
		return nil, 0, 0, err
	}
	findings := make([]auditreport.Finding, 0, len(rows))
	for _, row := range rows {
		f := auditreport.Finding{
			Title:      row.Title,
			Department: row.DepartmentName,
			Status:     row.Status,
			IssueID:    util.UUIDToString(row.IssueID),
		}
		if row.DueDate.Valid {
			f.DueDate = row.DueDate.Time.Format("2006-01-02")
		}
		findings = append(findings, f)
	}
	counts, err := q.CountWorkpapersForReport(ctx, db.CountWorkpapersForReportParams{
		ProjectID:   projectID,
		FiledStatus: auditmode.StatusFiled,
	})
	if err != nil {
		return nil, 0, 0, err
	}
	return findings, int(counts.Total), int(counts.Filed), nil
}

// findingsFor answers what a report shows: the snapshot once it is issued, the
// live ledger while it is a draft. One function, so a draft and an issued
// report can never be rendered by two different rules.
func (h *Handler) findingsFor(ctx context.Context, report db.AuditReport) ([]auditreport.Finding, int, int, error) {
	if report.Status == auditreport.StatusIssued {
		var snapshot []auditreport.Finding
		if err := json.Unmarshal(report.FindingsSnapshot, &snapshot); err != nil {
			return nil, 0, 0, err
		}
		return snapshot, int(report.WorkpaperCount), int(report.FiledWorkpaperCount), nil
	}
	return h.snapshotForReport(ctx, h.Queries, report.ProjectID)
}

func (h *Handler) auditReportResponse(ctx context.Context, report db.AuditReport) (AuditReportResponse, error) {
	findings, workpapers, filed, err := h.findingsFor(ctx, report)
	if err != nil {
		return AuditReportResponse{}, err
	}
	resp := AuditReportResponse{
		ID:              uuidToString(report.ID),
		ProjectID:       uuidToString(report.ProjectID),
		Version:         int(report.Version),
		Status:          report.Status,
		Title:           report.Title,
		Background:      report.Background,
		Basis:           report.Basis,
		Scope:           report.Scope,
		Opinion:         report.Opinion,
		Requirements:    report.Requirements,
		Findings:        findings,
		WorkpaperCount:  workpapers,
		FiledWorkpapers: filed,
		CreatedAt:       timestampToString(report.CreatedAt),
		UpdatedAt:       timestampToString(report.UpdatedAt),
	}
	if report.IssuedAt.Valid {
		resp.IssuedBy = uuidToString(report.IssuedBy)
		resp.IssuedAt = timestampToString(report.IssuedAt)
	}
	return resp, nil
}

// reportDocument assembles what the renderer needs, including the auditee and
// engagement names an exported report has to carry — a document that does not
// say who it is about is not a document anyone can file.
func (h *Handler) reportDocument(ctx context.Context, report db.AuditReport) (auditreport.Document, error) {
	findings, workpapers, filed, err := h.findingsFor(ctx, report)
	if err != nil {
		return auditreport.Document{}, err
	}
	doc := auditreport.Document{
		Title:           report.Title,
		Version:         int(report.Version),
		Status:          report.Status,
		Background:      report.Background,
		Basis:           report.Basis,
		Scope:           report.Scope,
		Opinion:         report.Opinion,
		Requirements:    report.Requirements,
		Findings:        findings,
		Workpapers:      workpapers,
		FiledWorkpapers: filed,
	}
	if project, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID: report.ProjectID, WorkspaceID: report.WorkspaceID,
	}); err == nil {
		doc.Engagement = project.Title
		if project.AuditPeriodStart.Valid {
			doc.PeriodStart = project.AuditPeriodStart.Time.Format("2006-01-02")
		}
		if project.AuditPeriodEnd.Valid {
			doc.PeriodEnd = project.AuditPeriodEnd.Time.Format("2006-01-02")
		}
	}
	if ws, err := h.Queries.GetWorkspace(ctx, report.WorkspaceID); err == nil {
		doc.Auditee = ws.Name
	}
	if report.IssuedAt.Valid {
		doc.IssuedAt = timestampToString(report.IssuedAt)
		doc.IssuedBy = h.memberDisplayName(ctx, report.IssuedBy)
	}
	return doc, nil
}

// memberDisplayName resolves a signatory to a name. A report signed by a UUID
// is a report nobody outside the system can read.
func (h *Handler) memberDisplayName(ctx context.Context, memberID pgtype.UUID) string {
	if !memberID.Valid {
		return ""
	}
	member, err := h.Queries.GetMember(ctx, memberID)
	if err != nil {
		return ""
	}
	user, err := h.Queries.GetUser(ctx, member.UserID)
	if err != nil {
		return ""
	}
	if name := strings.TrimSpace(user.Name); name != "" {
		return name
	}
	return user.Email
}

// recordReportTrail writes the report's step into the same trail as the
// workpaper approvals. No issue_id: a report is not an issue, and the entry
// names the report instead.
func (h *Handler) recordReportTrail(ctx context.Context, q *db.Queries, report db.AuditReport, decision auditreport.Decision, actorType, userID, reason string) error {
	details := map[string]any{
		"report_id":  uuidToString(report.ID),
		"project_id": uuidToString(report.ProjectID),
		"version":    report.Version,
		"to":         report.Status,
	}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		details["reason"] = trimmed
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	actorUUID, err := util.ParseUUID(userID)
	if err != nil {
		return err
	}
	_, err = q.CreateActivity(ctx, db.CreateActivityParams{
		ID:          dbid.NewV7(),
		WorkspaceID: report.WorkspaceID,
		ActorType:   pgtype.Text{String: actorType, Valid: actorType != ""},
		ActorID:     actorUUID,
		Action:      string(decision.Event),
		Details:     encoded,
	})
	return err
}

// textOrNull maps an absent key to "leave this section alone".
func textOrNull(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}
