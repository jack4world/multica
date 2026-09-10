-- Reshape step A: remove what the software needed and the audit does not.
--
-- 待采纳 (agent_delivered) was never a stage of an audit. It existed so an
-- agent's output had somewhere to sit, and it cost a status, a CLI command, a
-- scheduled job, this column, and a whole class of "nobody adopted this draft"
-- problem that the status itself created. An agent's account is a comment now,
-- and the workpaper stays in 编制中 until a person submits it.
--
-- ORDER MATTERS. Any workpaper currently sitting in 待采纳 is moved back to
-- 编制中 FIRST. Removing the status underneath it would strand it on a status
-- nothing produces and no picker offers — worse than the status was.
UPDATE issue SET status = 'drafting', updated_at = now()
WHERE status = 'agent_delivered'
  AND workspace_id IN (SELECT id FROM workspace WHERE audit_mode_enabled_at IS NOT NULL);

DELETE FROM issue_status WHERE key = 'agent_delivered' AND is_system = FALSE;

-- The reminder existed only for drafts sitting in that status.
ALTER TABLE audit_workpaper DROP COLUMN IF EXISTS handover_reminded_at;

-- The sensitivity label filtered nothing and said so. A field that looks like a
-- permission and is not one is worse than no field: somebody relies on it.
-- Access is auditee membership and nothing else (docs/adr/0001), and material
-- that must be kept separate goes in a separate auditee.
ALTER TABLE workspace DROP COLUMN IF EXISTS confidentiality;

-- The filing scheme is two levels: a section and its drawers. Four was chosen
-- because materialised paths make deep prefix reads cheap, which is true and
-- was not the question. Nothing shipped is lost — the seeded scheme is already
-- two deep — but a tree nobody can read is no longer representable.
ALTER TABLE audit_document_category DROP CONSTRAINT IF EXISTS audit_document_category_path_check;
ALTER TABLE audit_document_category ADD CONSTRAINT audit_document_category_path_check
    CHECK (path ~ '^[0-9]{2}(/[0-9]{2})?$') NOT VALID;
ALTER TABLE audit_document_category VALIDATE CONSTRAINT audit_document_category_path_check;

ALTER TABLE audit_document DROP CONSTRAINT IF EXISTS audit_document_category_path_check;
ALTER TABLE audit_document ADD CONSTRAINT audit_document_category_path_check
    CHECK (category_path ~ '^[0-9]{2}(/[0-9]{2})?$') NOT VALID;
ALTER TABLE audit_document VALIDATE CONSTRAINT audit_document_category_path_check;
