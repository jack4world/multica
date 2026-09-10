-- One filed piece of the auditee's material.
--
-- The ROW carries what audit cares about — where it is filed, what it is
-- called, who filed it. The BYTES live in a platform attachment, through the
-- storage the platform already configures, so there is one thing to back up.
--
-- Adding these columns to `attachment` was rejected: that table is shared by
-- issue attachments, comment attachments, agent uploads and channel media, and
-- audit semantics on it would touch four unrelated paths.
--
-- WHY NOT ON AN ISSUE. A voucher arrives before anyone knows which workpaper
-- will cite it, and once cited it is usually cited by several. Filing it under
-- one issue makes it invisible from the others and deletes it when that issue
-- goes — the audit file thinned by tidying up.
--
-- category_path is a value, not a foreign key: this schema has no foreign keys
-- (project rules), and the application refuses to delete a category that has
-- anything filed under it.
--
-- No primary key inline; see 472 and 473.
CREATE TABLE audit_document (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    attachment_id UUID NOT NULL,

    -- Exactly one category. Multiple filing is how "where does this live" stops
    -- having an answer; a document that belongs in two places is CITED from two
    -- workpapers, which is a different relationship.
    category_path TEXT NOT NULL CHECK (category_path ~ '^[0-9]{2}(/[0-9]{2}){0,3}$'),

    -- The auditor's own words. "IMG_4823.pdf" is not findable six months later.
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),

    uploader_type TEXT NOT NULL CHECK (uploader_type IN ('member', 'agent')),
    uploader_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
