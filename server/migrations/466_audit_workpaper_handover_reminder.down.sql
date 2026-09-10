-- Rows with no preparer cannot exist under the restored constraint; they are
-- reminder bookkeeping for drafts nobody submitted, and dropping them loses
-- only the "already reminded" flag.
DELETE FROM audit_workpaper WHERE preparer_id IS NULL;
ALTER TABLE audit_workpaper ALTER COLUMN preparer_id SET NOT NULL;
ALTER TABLE audit_workpaper DROP COLUMN IF EXISTS handover_reminded_at;
