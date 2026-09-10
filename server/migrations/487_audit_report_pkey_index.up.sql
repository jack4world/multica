-- Backing index for audit_report's primary key, attached in 488 via
-- PRIMARY KEY USING INDEX, per the repo's concurrent-index convention.
CREATE UNIQUE INDEX CONCURRENTLY audit_report_pkey_uidx
    ON audit_report (id);
