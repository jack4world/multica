-- name: IsWorkspaceAuditMode :one
-- Lean, NON-LOCKING read of the auditee flag for the review gate's write path
-- (the enable path uses GetWorkspaceAuditMode, which locks). Only reached once
-- the status keys have already proven an audit status is involved, so it costs
-- nothing on the ordinary issue write.
SELECT (audit_mode_enabled_at IS NOT NULL)::bool AS enabled
FROM workspace
WHERE id = $1;

-- name: LockIssueForReviewGate :one
-- Re-reads the issue under a row lock INSIDE the gate's transaction, so the
-- status and project the decision rests on are the ones the write lands on.
--
-- Without it the decision comes from a snapshot taken before the transaction
-- opened, and "a filed workpaper refuses every write" is only true when nobody
-- files it concurrently: a description edit could read 三级复核, be allowed,
-- then block on the row lock and commit onto a workpaper that is now filed.
SELECT * FROM issue
WHERE id = $1 AND workspace_id = $2
FOR UPDATE;

-- name: GetAuditRoleLevel :one
-- The actor's reviewer rank on ONE engagement. Unique on
-- (project_id, member_id), so there is never a second row to choose between.
SELECT level FROM audit_role
WHERE project_id = $1 AND member_id = $2;

-- name: ListAuditRolesForProject :many
SELECT id, workspace_id, project_id, member_id, level, created_at, created_by
FROM audit_role
WHERE project_id = $1
ORDER BY level ASC, created_at ASC;

-- name: SetAuditRole :one
-- Assigning a level to someone who already holds one REPLACES it rather than
-- failing: the unique constraint expresses "at most one level per person per
-- engagement", and a staffing change is the normal way that constraint is met.
INSERT INTO audit_role (workspace_id, project_id, member_id, level, created_by)
VALUES (
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('project_id')::uuid,
    sqlc.arg('member_id')::uuid,
    sqlc.arg('level')::text,
    sqlc.narg('created_by')::uuid
)
ON CONFLICT (project_id, member_id) DO UPDATE
SET level = EXCLUDED.level,
    created_by = EXCLUDED.created_by
RETURNING *;

-- name: DeleteAuditRole :execrows
DELETE FROM audit_role
WHERE project_id = $1 AND member_id = $2;

-- name: DeleteAuditRolesForProject :exec
-- Application-layer cleanup: there are no cascades, so deleting an engagement
-- has to take its roles with it. Scoped by workspace for the same reason
-- DeleteIssue is: a bare project_id predicate would reach another tenant's rows
-- if a caller ever passed a foreign id.
DELETE FROM audit_role
WHERE project_id = sqlc.arg('project_id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: GetWorkpaperPreparer :one
SELECT preparer_id FROM audit_workpaper
WHERE issue_id = $1;

-- name: RecordWorkpaperPreparer :exec
-- Written by the review gate at submission, inside the same transaction as the
-- status change, so a workpaper can never sit in review with no preparer on
-- record. A resubmission after rejection replaces the previous value.
INSERT INTO audit_workpaper (issue_id, workspace_id, preparer_id)
VALUES (
    sqlc.arg('issue_id')::uuid,
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('preparer_id')::uuid
)
ON CONFLICT (issue_id) DO UPDATE
SET preparer_id = EXCLUDED.preparer_id,
    submitted_at = now(),
    updated_at = now();

-- name: DeleteWorkpaperRecord :exec
DELETE FROM audit_workpaper WHERE issue_id = $1;
