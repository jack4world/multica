-- Backing index for audit_remediation's primary key, attached in 483. One row
-- per issue: an issue is on the ledger or it is not.
CREATE UNIQUE INDEX CONCURRENTLY audit_remediation_pkey_uidx
    ON audit_remediation (issue_id);
