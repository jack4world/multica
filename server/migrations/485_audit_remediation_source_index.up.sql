-- Next year's 后续审计: every item last year's engagement raised. Partial on
-- the items still open, because that is the question anyone asks of a closed
-- engagement.
CREATE INDEX CONCURRENTLY idx_audit_remediation_source_open
    ON audit_remediation (source_project_id)
    WHERE verified_at IS NULL;
