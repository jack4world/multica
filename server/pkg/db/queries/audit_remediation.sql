-- The 整改台账: the departments an auditee has, and the items they owe.

-- name: CreateAuditDepartment :one
INSERT INTO audit_department (workspace_id, name, position)
VALUES (
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('name')::text,
    sqlc.arg('position')::float8
)
RETURNING *;

-- name: ListAuditDepartments :many
SELECT * FROM audit_department
WHERE workspace_id = $1
ORDER BY position ASC, name ASC;

-- name: GetAuditDepartment :one
SELECT * FROM audit_department
WHERE id = $1 AND workspace_id = $2;

-- name: NextAuditDepartmentPosition :one
SELECT COALESCE(MAX(position), 0)::float8 + 1 AS next
FROM audit_department
WHERE workspace_id = $1;

-- name: CountRemediationInDepartment :one
-- What "can I delete this department" has to ask. Every item, not only the open
-- ones: a closed item still names the department that fixed it, and a report
-- that cannot resolve the name is a report with a hole in it.
SELECT COUNT(*)::bigint FROM audit_remediation
WHERE workspace_id = $1 AND department_id = $2;

-- name: DeleteAuditDepartment :execrows
DELETE FROM audit_department WHERE id = $1 AND workspace_id = $2;

-- name: CreateAuditRemediation :one
INSERT INTO audit_remediation (
    issue_id, workspace_id, source_project_id, source_issue_id, department_id
)
VALUES (
    sqlc.arg('issue_id')::uuid,
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('source_project_id')::uuid,
    sqlc.narg('source_issue_id'),
    sqlc.arg('department_id')::uuid
)
RETURNING *;

-- name: GetAuditRemediation :one
-- Read on the write path of every governed transition, by primary key. The
-- source engagement it returns is where the verifier's rank is read from.
SELECT * FROM audit_remediation WHERE issue_id = $1;

-- name: UpdateAuditRemediationDepartment :one
UPDATE audit_remediation
SET department_id = sqlc.arg('department_id')::uuid,
    updated_at = now()
WHERE issue_id = sqlc.arg('issue_id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid
RETURNING *;

-- name: RecordRemediationVerification :exec
-- Written in the SAME transaction as the status change it describes, so an item
-- can never read as closed with nobody recorded as having closed it.
UPDATE audit_remediation
SET verified_by = sqlc.arg('verified_by')::uuid,
    verified_at = now(),
    verification_note = sqlc.arg('verification_note')::text,
    updated_at = now()
WHERE issue_id = sqlc.arg('issue_id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: ListAuditRemediation :many
-- The ledger. Filters are all optional and all AND-ed; `overdue` narrows to
-- items past their deadline that are not closed, which is derived here rather
-- than stored because a stored flag is only as true as the last job run.
SELECT r.*, i.title, i.status, i.due_date, i.assignee_type, i.assignee_id,
       d.name AS department_name,
       p.title AS source_project_title
FROM audit_remediation r
JOIN issue i ON i.id = r.issue_id
JOIN audit_department d ON d.id = r.department_id
JOIN project p ON p.id = r.source_project_id
WHERE r.workspace_id = sqlc.arg('workspace_id')::uuid
  AND (sqlc.narg('department_id')::uuid IS NULL OR r.department_id = sqlc.narg('department_id'))
  AND (sqlc.narg('source_project_id')::uuid IS NULL OR r.source_project_id = sqlc.narg('source_project_id'))
  AND (sqlc.narg('status')::text IS NULL OR i.status = sqlc.narg('status'))
  AND (sqlc.narg('assignee_id')::uuid IS NULL OR i.assignee_id = sqlc.narg('assignee_id'))
  AND (
      NOT sqlc.arg('overdue_only')::bool
      OR (r.verified_at IS NULL
          AND i.due_date IS NOT NULL
          AND i.due_date < CURRENT_DATE
          AND i.status <> sqlc.arg('closed_status')::text)
  )
ORDER BY i.due_date ASC NULLS LAST, r.created_at DESC
LIMIT sqlc.arg('lim');

-- name: ListRemediationToRemind :many
-- Newly overdue items nobody has been told about yet. `overdue_reminded_at IS
-- NULL` is the whole of "once": an item that has been reminded is never
-- reminded again, however long it stays overdue.
SELECT r.issue_id, r.workspace_id, r.source_project_id, r.department_id,
       i.title, i.due_date, i.assignee_type, i.assignee_id
FROM audit_remediation r
JOIN issue i ON i.id = r.issue_id
WHERE r.overdue_reminded_at IS NULL
  AND r.verified_at IS NULL
  AND i.due_date IS NOT NULL
  AND i.due_date < CURRENT_DATE
  AND i.status <> sqlc.arg('closed_status')::text
  AND i.status <> 'cancelled'
ORDER BY i.due_date ASC
LIMIT sqlc.arg('lim');

-- name: MarkRemediationOverdueReminded :exec
UPDATE audit_remediation
SET overdue_reminded_at = now(), updated_at = now()
WHERE issue_id = $1;
