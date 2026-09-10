-- One version number per engagement, and the index the version list reads.
-- Two reports claiming to be version 2 of the same audit is a filing problem
-- nobody can resolve after the fact.
CREATE UNIQUE INDEX CONCURRENTLY audit_report_project_version_uidx
    ON audit_report (project_id, version);
