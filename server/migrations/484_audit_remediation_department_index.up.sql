-- The ledger read: an auditee's items, filtered by responsible department.
-- Also what proves a department still owns items when someone tries to delete
-- it.
CREATE INDEX CONCURRENTLY idx_audit_remediation_workspace_department
    ON audit_remediation (workspace_id, department_id);
