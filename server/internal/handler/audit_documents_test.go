package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auditdocs"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// The document library. Path handling is the canonical property of
// internal/auditdocs and is not replayed here; what needs a database is the
// tree's rules, the browse that motivated the path storage, and who may remove
// material from the audit file.

func (f auditFixture) categories(t *testing.T) []AuditCategoryResponse {
	t.Helper()
	var out []AuditCategoryResponse
	testutil.Call(t, testHandler.ListAuditCategories,
		auditRequest(http.MethodGet, "/api/audit/categories", f.workspaceID, nil)).
		Want(http.StatusOK).JSON(&out)
	return out
}

// attachmentIn creates a platform attachment in this auditee, standing in for
// an upload through the endpoint that already exists.
func (f auditFixture) attachmentIn(t *testing.T, filename string) string {
	t.Helper()
	return dbfx.Insert(t, "attachment", testutil.Cols{
		"workspace_id":  f.workspaceID,
		"uploader_type": "member",
		"uploader_id":   testUserID,
		"filename":      filename,
		"url":           "https://example.invalid/" + filename,
		"content_type":  "application/pdf",
		"size_bytes":    1024,
	})
}

func (f auditFixture) fileDoc(t *testing.T, path, title, attachmentID string) *testutil.Response {
	t.Helper()
	return testutil.Call(t, testHandler.FileAuditDocument,
		auditRequest(http.MethodPost, "/api/audit/documents", f.workspaceID, map[string]any{
			"attachment_id": attachmentID,
			"category_path": path,
			"title":         title,
		}))
}

func (f auditFixture) docsUnder(t *testing.T, path string) []AuditDocumentResponse {
	t.Helper()
	var out []AuditDocumentResponse
	testutil.Call(t, testHandler.ListAuditDocuments,
		auditRequest(http.MethodGet, "/api/audit/documents?category="+path, f.workspaceID, nil)).
		Want(http.StatusOK).JSON(&out)
	return out
}

// Every auditee starts from the same scheme, so anyone moving between clients
// finds the same drawers in the same places.
func TestANewAuditeeStartsWithTheStandardFilingScheme(t *testing.T) {
	f := newAuditFixture(t)
	dbfx.Cleanup(t, `DELETE FROM audit_document_category WHERE workspace_id = $1`, f.workspaceID)

	got := f.categories(t)
	if len(got) != len(auditdocs.StandardScheme()) {
		t.Fatalf("categories = %d, want the %d seeded", len(got), len(auditdocs.StandardScheme()))
	}
	// In filing order, and every node marked as standard.
	for i := 1; i < len(got); i++ {
		if got[i-1].Path >= got[i].Path {
			t.Errorf("out of filing order at %d: %q then %q", i, got[i-1].Path, got[i].Path)
		}
	}
	for _, c := range got {
		if !c.IsStandard {
			t.Errorf("seeded category %q is not marked standard", c.Path)
		}
	}
}

// Enabling is called once per workspace, but the seeding is also the path an
// auditee that predates the scheme would take. Running it twice must not double
// the tree.
func TestSeedingTheSchemeTwiceDoesNotDoubleIt(t *testing.T) {
	f := newAuditFixture(t)
	dbfx.Cleanup(t, `DELETE FROM audit_document_category WHERE workspace_id = $1`, f.workspaceID)
	before := len(f.categories(t))

	enableAuditMode(t, f.workspaceID, nil).Want(http.StatusOK)

	if after := len(f.categories(t)); after != before {
		t.Errorf("categories went from %d to %d on a second enable", before, after)
	}
}

// THE property the path storage exists for: asking for a parent returns
// everything beneath it, at any depth.
func TestAskingForAParentReturnsEverythingBeneathIt(t *testing.T) {
	f := newAuditFixture(t)
	dbfx.Cleanup(t, `DELETE FROM audit_document WHERE workspace_id = $1`, f.workspaceID)
	dbfx.Cleanup(t, `DELETE FROM audit_document_category WHERE workspace_id = $1`, f.workspaceID)

	f.fileDoc(t, "03/01", "记账凭证 2025-03", f.attachmentIn(t, "je.pdf")).Want(http.StatusCreated)
	f.fileDoc(t, "03/02", "付款单据", f.attachmentIn(t, "src.pdf")).Want(http.StatusCreated)
	f.fileDoc(t, "04/01", "采购合同", f.attachmentIn(t, "po.pdf")).Want(http.StatusCreated)

	under03 := f.docsUnder(t, "03")
	if len(under03) != 2 {
		t.Errorf("under 03 = %d documents, want 2 (both sub-categories)", len(under03))
	}
	for _, d := range under03 {
		if d.CategoryPath == "04/01" {
			t.Error("a document from another top-level category came back")
		}
	}

	leaf := f.docsUnder(t, "03/01")
	if len(leaf) != 1 {
		t.Errorf("under 03/01 = %d documents, want just that leaf's", len(leaf))
	}
}

// Orphaning material by tidying the tree is the failure that would matter most
// here, and it is silent: the rows stay, pointing at a path nothing lists.
func TestACategoryHoldingMaterialCannotBeDeleted(t *testing.T) {
	f := newAuditFixture(t)
	dbfx.Cleanup(t, `DELETE FROM audit_document WHERE workspace_id = $1`, f.workspaceID)
	dbfx.Cleanup(t, `DELETE FROM audit_document_category WHERE workspace_id = $1`, f.workspaceID)
	f.fileDoc(t, "03/01", "记账凭证", f.attachmentIn(t, "je.pdf")).Want(http.StatusCreated)

	req := auditRequest(http.MethodDelete, "/api/audit/categories/03%2F01", f.workspaceID, nil)
	testutil.Call(t, testHandler.DeleteAuditCategory, withURLParam(req, "path", "03/01")).
		Want(http.StatusConflict)

	// And the parent too, because the count reaches down.
	req2 := auditRequest(http.MethodDelete, "/api/audit/categories/03", f.workspaceID, nil)
	testutil.Call(t, testHandler.DeleteAuditCategory, withURLParam(req2, "path", "03")).
		Want(http.StatusConflict)
}

func TestACategoryWithSubCategoriesCannotBeDeleted(t *testing.T) {
	f := newAuditFixture(t)
	dbfx.Cleanup(t, `DELETE FROM audit_document_category WHERE workspace_id = $1`, f.workspaceID)

	req := auditRequest(http.MethodDelete, "/api/audit/categories/03", f.workspaceID, nil)
	testutil.Call(t, testHandler.DeleteAuditCategory, withURLParam(req, "path", "03")).
		Want(http.StatusConflict)
}

func TestAnEmptyCategoryCanBeDeleted(t *testing.T) {
	f := newAuditFixture(t)
	dbfx.Cleanup(t, `DELETE FROM audit_document_category WHERE workspace_id = $1`, f.workspaceID)

	req := auditRequest(http.MethodDelete, "/api/audit/categories/07", f.workspaceID, nil)
	testutil.Call(t, testHandler.DeleteAuditCategory, withURLParam(req, "path", "07")).
		WantOneOf(http.StatusNoContent, http.StatusOK)
}

// A drawer inside a cabinet that does not exist is a hole in the middle of the
// tree: the parent's prefix read returns something the parent's own listing
// does not.
func TestACategoryCannotBeCreatedWithoutItsParent(t *testing.T) {
	f := newAuditFixture(t)
	dbfx.Cleanup(t, `DELETE FROM audit_document_category WHERE workspace_id = $1`, f.workspaceID)

	testutil.Call(t, testHandler.CreateAuditCategory,
		auditRequest(http.MethodPost, "/api/audit/categories", f.workspaceID,
			map[string]any{"path": "99/01", "name": "孤儿"})).
		Want(http.StatusBadRequest)
}

// Filing into a drawer that does not exist puts material where no browse will
// find it — the same outcome as not filing it at all.
func TestADocumentCannotBeFiledIntoAMissingCategory(t *testing.T) {
	f := newAuditFixture(t)
	f.fileDoc(t, "98", "无处安放", f.attachmentIn(t, "x.pdf")).Want(http.StatusBadRequest)
}

// Without the check a document could point at another workspace's bytes, and
// the library would serve them to everyone here.
func TestADocumentCannotPointAtAnotherWorkspacesAttachment(t *testing.T) {
	f := newAuditFixture(t)
	other := newAuditWorkspace(t)
	foreign := dbfx.Insert(t, "attachment", testutil.Cols{
		"workspace_id":  other,
		"uploader_type": "member",
		"uploader_id":   testUserID,
		"filename":      "foreign.pdf",
		"url":           "https://example.invalid/foreign.pdf",
		"content_type":  "application/pdf",
		"size_bytes":    10,
	})
	f.fileDoc(t, "03/01", "别人的凭证", foreign).Want(http.StatusBadRequest)
}

// Material leaving the audit file is exactly as accountable as material
// entering it, and an agent cannot be held to that.
func TestAnAgentCanReadTheLibraryButNotRemoveFromIt(t *testing.T) {
	f := newAuditFixture(t)
	dbfx.Cleanup(t, `DELETE FROM audit_document WHERE workspace_id = $1`, f.workspaceID)
	var doc AuditDocumentResponse
	f.fileDoc(t, "03/01", "记账凭证", f.attachmentIn(t, "je.pdf")).
		Want(http.StatusCreated).JSON(&doc)

	read := auditRequest(http.MethodGet, "/api/audit/documents?category=03", f.workspaceID, nil)
	read.Header.Set("X-Actor-Source", "task_token")
	read.Header.Set("X-Agent-ID", uuid.NewString())
	testutil.Call(t, testHandler.ListAuditDocuments, read).Want(http.StatusOK)

	del := auditRequest(http.MethodDelete, "/api/audit/documents/"+doc.ID, f.workspaceID, nil)
	del.Header.Set("X-Actor-Source", "task_token")
	del.Header.Set("X-Agent-ID", uuid.NewString())
	testutil.Call(t, testHandler.DeleteAuditDocument, withURLParam(del, "id", doc.ID)).
		Want(http.StatusForbidden)

	if n := dbfx.Count(t, `SELECT COUNT(*) FROM audit_document WHERE id = $1`, doc.ID); n != 1 {
		t.Error("an agent removed material from the audit file")
	}
}

func TestOnlyOwnersAndAdminsChangeTheFilingScheme(t *testing.T) {
	f := newAuditFixture(t)
	userID := dbfx.User(t, "Plain", fmt.Sprintf("docs-%s@multica.ai", uuid.NewString()[:8]))
	dbfx.Member(t, f.workspaceID, userID, "member")

	req := auditRequest(http.MethodPost, "/api/audit/categories", f.workspaceID,
		map[string]any{"path": "08", "name": "自定义"})
	req.Header.Set("X-User-ID", userID)
	testutil.Call(t, testHandler.CreateAuditCategory, req).Want(http.StatusForbidden)
}
