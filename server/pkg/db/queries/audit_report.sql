-- The engagement's deliverable.

-- name: CreateAuditReport :one
INSERT INTO audit_report (workspace_id, project_id, version, title, created_by)
VALUES (
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('project_id')::uuid,
    sqlc.arg('version')::int,
    sqlc.arg('title')::text,
    sqlc.arg('created_by')::uuid
)
RETURNING *;

-- name: NextAuditReportVersion :one
SELECT COALESCE(MAX(version), 0)::int + 1 AS next
FROM audit_report
WHERE project_id = $1;

-- name: GetAuditReport :one
SELECT * FROM audit_report WHERE id = $1 AND workspace_id = $2;

-- name: LockAuditReport :one
-- Re-read under a row lock inside the write's own transaction. Deciding from
-- the snapshot the handler loaded makes issued-immutability only as true as the
-- absence of a concurrent signature.
SELECT * FROM audit_report WHERE id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: ListAuditReportsForProject :many
SELECT * FROM audit_report
WHERE project_id = $1 AND workspace_id = $2
ORDER BY version DESC;

-- name: UpdateAuditReportSections :one
UPDATE audit_report SET
    title = COALESCE(sqlc.narg('title'), title),
    background = COALESCE(sqlc.narg('background'), background),
    basis = COALESCE(sqlc.narg('basis'), basis),
    scope = COALESCE(sqlc.narg('scope'), scope),
    opinion = COALESCE(sqlc.narg('opinion'), opinion),
    requirements = COALESCE(sqlc.narg('requirements'), requirements),
    updated_at = now()
WHERE id = sqlc.arg('id')::uuid AND workspace_id = sqlc.arg('workspace_id')::uuid
RETURNING *;

-- name: SetAuditReportStatus :one
UPDATE audit_report SET status = sqlc.arg('status')::text, updated_at = now()
WHERE id = sqlc.arg('id')::uuid AND workspace_id = sqlc.arg('workspace_id')::uuid
RETURNING *;

-- name: IssueAuditReport :one
-- The signature and the snapshot are one write. A report that reads as issued
-- while still rendering from live rows is the failure the snapshot exists to
-- prevent.
UPDATE audit_report SET
    status = 'issued',
    issued_by = sqlc.arg('issued_by')::uuid,
    issued_at = now(),
    findings_snapshot = sqlc.arg('findings_snapshot'),
    workpaper_count = sqlc.arg('workpaper_count')::int,
    filed_workpaper_count = sqlc.arg('filed_workpaper_count')::int,
    updated_at = now()
WHERE id = sqlc.arg('id')::uuid AND workspace_id = sqlc.arg('workspace_id')::uuid
RETURNING *;

-- name: ListRemediationForReport :many
-- The items this engagement raised, in the order a report lists them. Read live
-- while the report is a draft, and snapshotted at signing.
SELECT r.issue_id, i.title, i.status, i.due_date, d.name AS department_name,
       i.assignee_type, i.assignee_id, r.verified_by
FROM audit_remediation r
JOIN issue i ON i.id = r.issue_id
JOIN audit_department d ON d.id = r.department_id
WHERE r.source_project_id = $1
ORDER BY i.due_date ASC NULLS LAST, r.created_at ASC;

-- name: CountWorkpapersForReport :one
-- What stood behind the report: every workpaper in the engagement, and how many
-- reached the terminal filed state. A claim about the work has to be checkable.
SELECT COUNT(*)::bigint AS total,
       COUNT(*) FILTER (WHERE status = sqlc.arg('filed_status')::text)::bigint AS filed
FROM issue
WHERE project_id = $1;

-- name: DeleteAuditReportsForProject :exec
DELETE FROM audit_report WHERE project_id = $1;

-- name: GetIssuedAuditReport :one
-- The report the archive is built around. An engagement with no issued report
-- is not an audit anyone can file.
SELECT * FROM audit_report
WHERE project_id = $1 AND workspace_id = $2 AND status = 'issued'
ORDER BY version DESC
LIMIT 1;
