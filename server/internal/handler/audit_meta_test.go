package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/auditmeta"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// The boundary rules for an auditee's and an engagement's recorded facts. The
// validation matrix is the canonical property of internal/auditmeta and is not
// replayed here; this covers who may write them and — the one worth naming —
// that the confidentiality label changes nothing.

func TestTheAuditeesIdentityIsOwnerAdminOnlyAndNeverAnAgent(t *testing.T) {
	f := newAuditFixture(t)
	userID := dbfx.User(t, "Plain", fmt.Sprintf("meta-%s@multica.ai", uuid.NewString()[:8]))
	dbfx.Member(t, f.workspaceID, userID, "member")

	t.Run("plain member", func(t *testing.T) {
		req := auditRequest(http.MethodPut, "/api/workspaces/"+f.workspaceID, f.workspaceID,
			map[string]any{"client_name": "某某集团有限公司"})
		req.Header.Set("X-User-ID", userID)
		testutil.Call(t, testHandler.UpdateWorkspace, withURLParam(req, "id", f.workspaceID)).
			Want(http.StatusForbidden)
	})

	t.Run("agent actor", func(t *testing.T) {
		req := auditRequest(http.MethodPut, "/api/workspaces/"+f.workspaceID, f.workspaceID,
			map[string]any{"client_name": "某某集团有限公司"})
		req.Header.Set("X-Actor-Source", "task_token")
		req.Header.Set("X-Agent-ID", uuid.NewString())
		testutil.Call(t, testHandler.UpdateWorkspace, withURLParam(req, "id", f.workspaceID)).
			Want(http.StatusForbidden)
	})

	var stored *string
	dbfx.QueryRow(t, `SELECT client_name FROM workspace WHERE id = $1`, f.workspaceID).Scan(&stored)
	if stored != nil {
		t.Errorf("client_name = %q after two refused writes", *stored)
	}
}

func TestAnOwnerRecordsTheAuditedEntityAndItsLabel(t *testing.T) {
	f := newAuditFixture(t)
	req := auditRequest(http.MethodPut, "/api/workspaces/"+f.workspaceID, f.workspaceID, map[string]any{
		"client_name":     "某某集团有限公司",
		"confidentiality": auditmeta.ConfidentialityRestricted,
	})
	var out WorkspaceResponse
	testutil.Call(t, testHandler.UpdateWorkspace, withURLParam(req, "id", f.workspaceID)).
		Want(http.StatusOK).JSON(&out)

	if out.ClientName == nil || *out.ClientName != "某某集团有限公司" {
		t.Errorf("client_name = %v", out.ClientName)
	}
	if out.Confidentiality == nil || *out.Confidentiality != auditmeta.ConfidentialityRestricted {
		t.Errorf("confidentiality = %v", out.Confidentiality)
	}
}

// THE test about something not happening. Nothing else in the system would
// notice if the label quietly acquired teeth, and a second isolation model
// that disagreed with membership is exactly what ADR-0001 exists to prevent.
func TestTheConfidentialityLabelChangesNothingAnyoneCanSee(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusDrafting)

	before := len(f.queueFor(t, f.reviewerUsers[auditgate.LevelL1]))
	visibleBefore := dbfx.Count(t, `SELECT COUNT(*) FROM issue WHERE workspace_id = $1`, f.workspaceID)

	req := auditRequest(http.MethodPut, "/api/workspaces/"+f.workspaceID, f.workspaceID,
		map[string]any{"confidentiality": auditmeta.ConfidentialitySecret})
	testutil.Call(t, testHandler.UpdateWorkspace, withURLParam(req, "id", f.workspaceID)).Want(http.StatusOK)

	if got := dbfx.Count(t, `SELECT COUNT(*) FROM issue WHERE workspace_id = $1`, f.workspaceID); got != visibleBefore {
		t.Errorf("issue count changed from %d to %d after labelling the auditee secret", visibleBefore, got)
	}
	if got := len(f.queueFor(t, f.reviewerUsers[auditgate.LevelL1])); got != before {
		t.Errorf("review queue changed from %d to %d after labelling the auditee secret", before, got)
	}
	// And the workpaper is still readable by an ordinary member.
	req2 := auditRequest(http.MethodGet, "/api/issues/"+wp, f.workspaceID, nil)
	testutil.Call(t, testHandler.GetIssue, withURLParam(req2, "id", wp)).Want(http.StatusOK)
}

func TestAnImpossibleAuditPeriodIsRefused(t *testing.T) {
	f := newAuditFixture(t)
	req := auditRequest(http.MethodPut, "/api/projects/"+f.projectID, f.workspaceID, map[string]any{
		"audit_period_start": "2025-12-31",
		"audit_period_end":   "2025-01-01",
	})
	testutil.Call(t, testHandler.UpdateProject, withURLParam(req, "id", f.projectID)).
		Want(http.StatusBadRequest)
}

// Correcting one end must be validated against the stored other end, or a
// request that looks fine on its own lands an impossible period.
func TestCorrectingOneEndIsCheckedAgainstTheStoredOther(t *testing.T) {
	f := newAuditFixture(t)
	set := auditRequest(http.MethodPut, "/api/projects/"+f.projectID, f.workspaceID, map[string]any{
		"audit_period_start": "2025-01-01",
		"audit_period_end":   "2025-12-31",
	})
	testutil.Call(t, testHandler.UpdateProject, withURLParam(set, "id", f.projectID)).Want(http.StatusOK)

	// On its own this end is a valid date; against the stored start it is not.
	bad := auditRequest(http.MethodPut, "/api/projects/"+f.projectID, f.workspaceID,
		map[string]any{"audit_period_end": "2024-06-30"})
	testutil.Call(t, testHandler.UpdateProject, withURLParam(bad, "id", f.projectID)).
		Want(http.StatusBadRequest)

	var stored *string
	dbfx.QueryRow(t, `SELECT audit_period_end::text FROM project WHERE id = $1`, f.projectID).Scan(&stored)
	if stored == nil || *stored != "2025-12-31" {
		t.Errorf("audit_period_end = %v, want the refused write to have changed nothing", stored)
	}
}

func TestAnUnknownAuditTypeIsRefused(t *testing.T) {
	f := newAuditFixture(t)
	req := auditRequest(http.MethodPut, "/api/projects/"+f.projectID, f.workspaceID,
		map[string]any{"audit_type": "离任审计"})
	testutil.Call(t, testHandler.UpdateProject, withURLParam(req, "id", f.projectID)).
		Want(http.StatusBadRequest)
}

func TestTheEngagementsFactsAreEditableByAProjectEditor(t *testing.T) {
	f := newAuditFixture(t)
	req := auditRequest(http.MethodPut, "/api/projects/"+f.projectID, f.workspaceID, map[string]any{
		"audit_period_start": "2025-01-01",
		"audit_period_end":   "2025-12-31",
		"audit_type":         auditmeta.AuditTypeSeparationOfOffice,
	})
	var out ProjectResponse
	testutil.Call(t, testHandler.UpdateProject, withURLParam(req, "id", f.projectID)).
		Want(http.StatusOK).JSON(&out)

	if out.AuditType == nil || *out.AuditType != auditmeta.AuditTypeSeparationOfOffice {
		t.Errorf("audit_type = %v", out.AuditType)
	}
	if out.AuditPeriodStart == nil || *out.AuditPeriodStart != "2025-01-01" {
		t.Errorf("audit_period_start = %v", out.AuditPeriodStart)
	}
}

// An ordinary project edit must not silently clear an audit period nobody
// mentioned.
func TestAnOrdinaryProjectEditLeavesTheAuditPeriodAlone(t *testing.T) {
	f := newAuditFixture(t)
	set := auditRequest(http.MethodPut, "/api/projects/"+f.projectID, f.workspaceID,
		map[string]any{"audit_period_start": "2025-03-01"})
	testutil.Call(t, testHandler.UpdateProject, withURLParam(set, "id", f.projectID)).Want(http.StatusOK)

	rename := auditRequest(http.MethodPut, "/api/projects/"+f.projectID, f.workspaceID,
		map[string]any{"title": "Renamed engagement"})
	var out ProjectResponse
	testutil.Call(t, testHandler.UpdateProject, withURLParam(rename, "id", f.projectID)).
		Want(http.StatusOK).JSON(&out)

	if out.AuditPeriodStart == nil || *out.AuditPeriodStart != "2025-03-01" {
		t.Errorf("audit_period_start = %v after an unrelated rename", out.AuditPeriodStart)
	}
}
