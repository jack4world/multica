-- Backing index for audit_department's primary key, attached in 479 via
-- PRIMARY KEY USING INDEX, per the repo's concurrent-index convention.
CREATE UNIQUE INDEX CONCURRENTLY audit_department_pkey_uidx
    ON audit_department (id);
