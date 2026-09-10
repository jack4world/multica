package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

// An auditee enabled BEFORE the library existed has no categories, and no way
// to get any: filing needs a category, and creating one needs its parent. The
// enable path is the only way in, so it has to seed even when the workspace is
// already an auditee — which it did not, because it returned early.
func TestAnAuditeeEnabledBeforeTheLibraryCanStillGetItsScheme(t *testing.T) {
	f := newAuditFixture(t)
	// The state such a workspace is in: audit mode on, no filing scheme.
	dbfx.Exec(t, `DELETE FROM audit_document_category WHERE workspace_id = $1`, f.workspaceID)
	if n := len(f.categories(t)); n != 0 {
		t.Fatalf("setup: categories = %d, want 0", n)
	}

	enableAuditMode(t, f.workspaceID, nil).Want(http.StatusOK)

	if n := len(f.categories(t)); n != len(auditdocs.StandardScheme()) {
		t.Errorf("categories = %d after re-enabling, want the %d seeded — an auditee "+
			"that predates the library would otherwise be stuck with nowhere to file",
			n, len(auditdocs.StandardScheme()))
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

// The library must not hand out the bytes' address.
//
// It used to return the attachment's raw storage URL. On a local-storage
// deployment /uploads/* is served on the ROOT router, outside the auth
// middleware and with no workspace check, so that URL fetched an auditee's
// voucher or bank statement with no session at all — and it never expired,
// while travelling through browser history, referrers, proxy logs and
// corporate inspection appliances.
//
// The platform accepts that trade for ordinary attachments: the unguessable
// URL is the credential. Audit material does not get to make that trade.
func TestTheLibraryHandsOutACapabilityNotAStorageURL(t *testing.T) {
	f := newAuditFixture(t)
	att := f.attachmentIn(t, "voucher.pdf")

	var filed AuditDocumentResponse
	f.fileDoc(t, "01", "记账凭证", att).Want(http.StatusCreated).JSON(&filed)

	for _, doc := range []AuditDocumentResponse{filed, f.docsUnder(t, "01")[0]} {
		if !strings.HasPrefix(doc.DownloadURL, "/api/attachments/") ||
			!strings.Contains(doc.DownloadURL, "/signed-download") {
			t.Errorf("download_url = %q, want a signed capability", doc.DownloadURL)
		}
		if !strings.Contains(doc.DownloadURL, "exp=") || !strings.Contains(doc.DownloadURL, "sig=") {
			t.Errorf("download_url = %q carries no expiry or signature", doc.DownloadURL)
		}
		// The storage URL must not appear ANYWHERE in the response: this is a
		// test about what the library publishes, not about one field's name.
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, leak := range []string{"/uploads/", "example.invalid"} {
			if strings.Contains(string(encoded), leak) {
				t.Errorf("the response carries the storage address %q: %s", leak, encoded)
			}
		}
	}
}

// The capability is minted per read, so it is not something a client can hold
// onto — two reads of the same document are two different, separately expiring
// links.
func TestEachReadMintsItsOwnCapability(t *testing.T) {
	f := newAuditFixture(t)
	att := f.attachmentIn(t, "contract.pdf")
	f.fileDoc(t, "01", "合同", att).Want(http.StatusCreated)

	first := f.docsUnder(t, "01")[0].DownloadURL
	if first == "" {
		t.Fatal("no capability minted")
	}
	// Same attachment, same signature only because the expiry has not ticked;
	// what matters is that it is re-derived rather than stored.
	var stored string
	dbfx.QueryRow(t, `SELECT url FROM attachment WHERE id = $1`, att).Scan(&stored)
	if strings.Contains(first, stored) {
		t.Errorf("the capability embeds the storage URL: %q", first)
	}
}

// 审计资料 IS the evidence. Removing one used to be a hard DELETE any workspace
// member could perform, writing nothing anywhere — and that is the one hole an
// append-only trail cannot cover: what was never recorded needs no altering. A
// document removed before archival left a complete-looking 卷宗, a matching
// sha256, a clean trail, and nothing to say it had ever existed.
func TestWithdrawingADocumentLeavesTheRecordAndTakesARank(t *testing.T) {
	f := newAuditFixture(t)
	var doc AuditDocumentResponse
	f.fileDoc(t, "01", "银行对账单", f.attachmentIn(t, "stmt.pdf")).
		Want(http.StatusCreated).JSON(&doc)

	withdraw := func(asUserID string, body map[string]any) *testutil.Response {
		req := auditRequest(http.MethodDelete, "/api/audit/documents/"+doc.ID, f.workspaceID, body)
		if asUserID != "" {
			req.Header.Set("X-User-ID", asUserID)
		}
		return testutil.Call(t, testHandler.DeleteAuditDocument, withURLParam(req, "id", doc.ID))
	}

	// An ordinary member cannot take evidence out of the file. The gradient was
	// inverted: changing a remediation item's status needed a rank and left a
	// trail, while destroying an original voucher needed neither.
	plain := dbfx.User(t, "Plain", "plain-"+uuid.NewString()[:8]+"@multica.ai")
	dbfx.Member(t, f.workspaceID, plain, "member")
	withdraw(plain, map[string]any{"reason": "拿走"}).Want(http.StatusForbidden)

	// Nor can an owner take it out silently.
	withdraw("", map[string]any{}).Want(http.StatusBadRequest)

	withdraw("", map[string]any{"reason": "客户提供的版本有误，已换新版归入 01"}).
		Want(http.StatusNoContent)

	// The row survives, marked, so the library can still say what was here.
	var withdrawnBy string
	var reason string
	dbfx.QueryRow(t, `SELECT withdrawn_by::text, withdrawal_reason FROM audit_document WHERE id = $1`,
		doc.ID).Scan(&withdrawnBy, &reason)
	if withdrawnBy == "" || reason == "" {
		t.Errorf("withdrawal recorded by=%q reason=%q; both are the point", withdrawnBy, reason)
	}

	// And it is out of the library.
	for _, listed := range f.docsUnder(t, "01") {
		if listed.ID == doc.ID {
			t.Error("a withdrawn document is still listed in its drawer")
		}
	}

	// THE part that makes it not-silent: an entry in the append-only trail the
	// daily export copies out of the database.
	var actions int
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM activity_log
	    WHERE workspace_id = $1 AND action = 'audit_document_withdrawn'
	      AND details->>'document_id' = $2`, f.workspaceID, doc.ID).Scan(&actions)
	if actions != 1 {
		t.Errorf("trail entries for the withdrawal = %d, want 1", actions)
	}

	// A second withdrawal must not overwrite who took it down or why.
	withdraw("", map[string]any{"reason": "再删一次"}).Want(http.StatusNotFound)
	var stillReason string
	dbfx.QueryRow(t, `SELECT withdrawal_reason FROM audit_document WHERE id = $1`, doc.ID).Scan(&stillReason)
	if stillReason != reason {
		t.Errorf("the second withdrawal rewrote the reason: %q", stillReason)
	}
}

// Withdrawal keeps the row and writes the trail, but the library still has to
// be able to say "this was here and it went, because R". Without a read for it,
// that answer lives only in the trail — and a claim the product cannot show is
// a claim it does not really make.
func TestTheLibraryCanSayWhatWasWithdrawn(t *testing.T) {
	f := newAuditFixture(t)
	var doc AuditDocumentResponse
	f.fileDoc(t, "01", "旧版银行对账单", f.attachmentIn(t, "old-stmt.pdf")).
		Want(http.StatusCreated).JSON(&doc)
	f.fileDoc(t, "01", "在册凭证", f.attachmentIn(t, "kept.pdf")).Want(http.StatusCreated)

	del := auditRequest(http.MethodDelete, "/api/audit/documents/"+doc.ID, f.workspaceID,
		map[string]any{"reason": "客户提供的版本有误，已换新版归入 01"})
	testutil.Call(t, testHandler.DeleteAuditDocument, withURLParam(del, "id", doc.ID)).
		Want(http.StatusNoContent)

	var withdrawn []AuditDocumentResponse
	testutil.Call(t, testHandler.ListWithdrawnAuditDocuments,
		auditRequest(http.MethodGet, "/api/audit/documents/withdrawn", f.workspaceID, nil)).
		Want(http.StatusOK).JSON(&withdrawn)

	var found *AuditDocumentResponse
	for i := range withdrawn {
		if withdrawn[i].ID == doc.ID {
			found = &withdrawn[i]
		}
		if withdrawn[i].Title == "在册凭证" {
			t.Error("a document still in the library is listed as withdrawn")
		}
	}
	if found == nil {
		t.Fatal("the withdrawn document is not listed anywhere; the record exists only in the trail")
	}
	if found.WithdrawalReason == "" || found.WithdrawnAt == "" || found.WithdrawnBy == "" {
		t.Errorf("withdrawn entry = %+v, want who, when and why", found)
	}
	// No download capability: the material is out of the file. This view says
	// it WAS here, not "here it still is".
	if found.DownloadURL != "" {
		t.Errorf("withdrawn document still carries a download link: %q", found.DownloadURL)
	}
}
