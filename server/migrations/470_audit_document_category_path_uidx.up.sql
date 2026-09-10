-- One category per path per auditee. Also the lookup that every browse uses:
-- (workspace_id, path) answers both "this node" and, as a prefix, "everything
-- under it".
CREATE UNIQUE INDEX CONCURRENTLY audit_document_category_path_uidx
    ON audit_document_category (workspace_id, path);
