-- When an engagement's file was closed, and where it is.
--
-- THE KEY IS RECORDED, not reconstructed. A path rebuilt from ids at read time
-- is a path that breaks the day the prefix scheme changes, and the file it
-- points at is the one an auditor is asking for.
--
-- Nullable: an engagement is unarchived until it is archived, and an ordinary
-- project never consults either column.
ALTER TABLE project ADD COLUMN audit_archived_at TIMESTAMPTZ;
ALTER TABLE project ADD COLUMN audit_archive_key TEXT;
