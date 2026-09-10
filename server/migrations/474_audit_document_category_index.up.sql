-- The browse. (workspace_id, category_path) answers "this category's documents"
-- as an equality and "everything under it" as a prefix, which is the whole
-- reason the tree is stored as paths.
CREATE INDEX CONCURRENTLY idx_audit_document_workspace_category
    ON audit_document (workspace_id, category_path, created_at DESC);
