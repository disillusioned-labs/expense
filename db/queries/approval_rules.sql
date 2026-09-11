-- name: CreateApprovalRule :one
INSERT INTO approval_rules (organization_id, project_id, approver_id, step, created_by)
VALUES ($1, $2, $3, $4, $5) RETURNING
    id, organization_id, project_id, approver_id, step, created_by, created_at, updated_at, deleted_at;

-- name: GetApprovalRule :one
SELECT id, organization_id, project_id, approver_id, step, created_by, created_at, updated_at, deleted_at
FROM approval_rules
WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL;

-- name: ListApprovalRules :many
SELECT id, organization_id, project_id, approver_id, step, created_by, created_at, updated_at, deleted_at
FROM approval_rules
WHERE organization_id = $1 AND deleted_at IS NULL
  AND (
    (sqlc.narg('project_id')::uuid IS NOT NULL AND project_id = sqlc.narg('project_id'))
        OR (sqlc.narg('project_id')::uuid IS NULL AND project_id IS NULL)
  )
ORDER BY step ASC, created_at ASC;

-- name: UpdateApprovalRuleStep :one
UPDATE approval_rules
SET step = $3
WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL RETURNING
    id, organization_id, project_id, approver_id, step, created_by, created_at, updated_at, deleted_at;

-- name: SoftDeleteApprovalRule :execrows
UPDATE approval_rules
SET deleted_at = now()
WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL;

-- name: CountDefaultOrgRules :one
SELECT COUNT(*) FROM approval_rules
WHERE organization_id = $1 AND project_id IS NULL AND deleted_at IS NULL;

-- name: ListRulesByApprover :many
SELECT id, organization_id, project_id, approver_id, step, created_by, created_at, updated_at, deleted_at
FROM approval_rules
WHERE organization_id = $1 AND approver_id = $2 AND deleted_at IS NULL;

-- name: ResolveProjectRules :many
SELECT id, organization_id, project_id, approver_id, step, created_by, created_at, updated_at, deleted_at
FROM approval_rules
WHERE organization_id = $1 AND project_id = $2 AND deleted_at IS NULL
ORDER BY step ASC, created_at ASC;

-- name: ResolveDefaultRules :many
SELECT id, organization_id, project_id, approver_id, step, created_by, created_at, updated_at, deleted_at
FROM approval_rules
WHERE organization_id = $1 AND project_id IS NULL AND deleted_at IS NULL
ORDER BY step ASC, created_at ASC;

-- name: CreateDefaultApprovalRule :one
INSERT INTO approval_rules (organization_id, project_id, approver_id, step, created_by)
VALUES ($1, NULL, $2, 1, $2) RETURNING
    id, organization_id, project_id, approver_id, step, created_by, created_at, updated_at, deleted_at;

-- name: ReassignApprovalRules :execrows
UPDATE approval_rules
SET approver_id = $3, updated_at = now()
WHERE organization_id = $1 AND approver_id = $2 AND deleted_at IS NULL;
