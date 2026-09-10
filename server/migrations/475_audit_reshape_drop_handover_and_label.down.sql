ALTER TABLE audit_document DROP CONSTRAINT IF EXISTS audit_document_category_path_check;
ALTER TABLE audit_document ADD CONSTRAINT audit_document_category_path_check
    CHECK (category_path ~ '^[0-9]{2}(/[0-9]{2}){0,3}$');
ALTER TABLE audit_document_category DROP CONSTRAINT IF EXISTS audit_document_category_path_check;
ALTER TABLE audit_document_category ADD CONSTRAINT audit_document_category_path_check
    CHECK (path ~ '^[0-9]{2}(/[0-9]{2}){0,3}$');
ALTER TABLE workspace ADD COLUMN IF NOT EXISTS confidentiality TEXT
    CHECK (confidentiality IS NULL OR confidentiality IN ('normal', 'restricted', 'secret'));
ALTER TABLE audit_workpaper ADD COLUMN IF NOT EXISTS handover_reminded_at TIMESTAMPTZ;
-- The status itself is not recreated: it is seeded per workspace by the
-- application, so a rollback that re-added a row here would create one the
-- seeder does not know about.
