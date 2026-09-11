-- name: CreateProject :one
INSERT INTO projects (organization_id, name, created_by)
VALUES ($1, $2, $3) RETURNING
    id, organization_id, name, created_by, created_at, updated_at, deleted_at, archived_at;

-- name: GetProject :one
SELECT id, organization_id, name, created_by, created_at, updated_at, deleted_at, archived_at
FROM projects
WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL;

-- name: ListProjects :many
SELECT id, organization_id, name, created_by, created_at, updated_at, deleted_at, archived_at
FROM projects
WHERE organization_id = $1
  AND deleted_at IS NULL
  AND (sqlc.arg('include_archived')::bool OR archived_at IS NULL)
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: UpdateProjectName :one
UPDATE projects
SET name = $2
WHERE id = $1 AND organization_id = $3 AND deleted_at IS NULL RETURNING
    id, organization_id, name, created_by, created_at, updated_at, deleted_at, archived_at;

-- name: CountProjectTransactions :one
SELECT COUNT(*) FROM transactions WHERE project_id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteProject :execrows
UPDATE projects
SET deleted_at = now()
WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL;

-- name: ArchiveProject :one
UPDATE projects
SET archived_at = now()
WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL AND archived_at IS NULL RETURNING
    id, organization_id, name, created_by, created_at, updated_at, deleted_at, archived_at;

-- name: UnarchiveProject :one
UPDATE projects
SET archived_at = NULL
WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL AND archived_at IS NOT NULL RETURNING
    id, organization_id, name, created_by, created_at, updated_at, deleted_at, archived_at;

-- name: UpsertProjectMember :one
INSERT INTO project_members (organization_id, project_id, user_id, role_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (project_id, user_id)
    DO UPDATE SET role_id = EXCLUDED.role_id RETURNING
        organization_id, project_id, user_id, role_id, created_at, updated_at;

-- name: GetProjectMember :one
SELECT organization_id, project_id, user_id, role_id, created_at, updated_at
FROM project_members
WHERE project_id = $1 AND user_id = $2;

-- name: DeleteProjectMember :execrows
DELETE FROM project_members WHERE project_id = $1 AND user_id = $2;

-- name: ListProjectMembers :many
SELECT pm.organization_id, pm.project_id, pm.user_id, pm.role_id, pm.created_at, pm.updated_at,
       r.name AS role_name, r.is_system AS role_is_system
FROM project_members pm
         JOIN roles r ON r.id = pm.role_id AND r.deleted_at IS NULL
WHERE pm.project_id = $1
ORDER BY pm.created_at ASC;

-- name: ListPlacedProjects :many
SELECT p.id, p.organization_id, p.name, p.created_by, p.created_at, p.updated_at, p.deleted_at, p.archived_at
FROM projects p
         JOIN project_members pm ON pm.project_id = p.id
WHERE p.organization_id = $1 AND pm.user_id = $2 AND p.deleted_at IS NULL
  AND (sqlc.arg('include_archived')::bool OR p.archived_at IS NULL)
ORDER BY p.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');
