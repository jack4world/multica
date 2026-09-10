-- One department per name per auditee, case-insensitively. This is the index
-- that makes counting by department mean something: two spellings of one
-- department are two rows in the report and one conversation nobody has.
CREATE UNIQUE INDEX CONCURRENTLY audit_department_name_uidx
    ON audit_department (workspace_id, lower(name));
