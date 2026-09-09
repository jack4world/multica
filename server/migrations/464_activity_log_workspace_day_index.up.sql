-- The daily audit trail export reads one auditee's activity for one UTC day.
-- activity_log's only indexes are keyed by issue (068) and by the squad
-- no-action probe (089), so that read is a sequential scan of one of the
-- largest tables in the database — once per auditee per day, and up to fourteen
-- more when the job catches up after an outage.
--
-- Partial on purpose. The predicate matches the export's WHERE clause, and it
-- keeps the index off deployments that never enable audit mode: activity_log is
-- written on every issue change, and a second full index on it would be paid
-- for by everyone to serve a feature almost nobody runs.
CREATE INDEX CONCURRENTLY idx_activity_log_workspace_created_at
    ON activity_log (workspace_id, created_at);
