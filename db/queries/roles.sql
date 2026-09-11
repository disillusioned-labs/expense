-- name: CreateRole :one
INSERT INTO roles (project_id, name, is_system, created_by)
VALUES ($1, $2, $3, $4) RETURNING
    id, project_id, name, is_system, created_by, created_at, updated_at, deleted_at;

-- name: GetRole :one
SELECT id, project_id, name, is_system, created_by, created_at, updated_at, deleted_at
FROM roles
WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL;

-- name: GetRoleByName :one
SELECT id, project_id, name, is_system, created_by, created_at, updated_at, deleted_at
FROM roles
WHERE project_id = $1 AND name = $2 AND deleted_at IS NULL;

-- name: ListRoles :many
SELECT id, project_id, name, is_system, created_by, created_at, updated_at, deleted_at
FROM roles
WHERE project_id = $1 AND deleted_at IS NULL
ORDER BY is_system DESC, name ASC;

-- name: UpdateRoleName :one
UPDATE roles
SET name = $2
WHERE id = $1 AND project_id = $3 AND deleted_at IS NULL RETURNING
    id, project_id, name, is_system, created_by, created_at, updated_at, deleted_at;

-- name: SoftDeleteRole :execrows
UPDATE roles
SET deleted_at = now()
WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL;

-- name: DeleteRolePermissions :exec
DELETE FROM role_permissions WHERE role_id = $1;

-- name: AddRolePermission :exec
INSERT INTO role_permissions (role_id, action) VALUES ($1, $2)
ON CONFLICT (role_id, action) DO NOTHING;

-- name: ListRoleActions :many
SELECT action FROM role_permissions WHERE role_id = $1 ORDER BY action;

-- name: CountRoleUsage :one
SELECT COUNT(*) FROM project_members WHERE role_id = $1;

-- name: ListRoleUsageProjects :many
SELECT p.id, p.name
FROM project_members pm
         JOIN projects p ON p.id = pm.project_id
WHERE pm.role_id = $1 AND p.deleted_at IS NULL
ORDER BY p.name;

-- name: GetUserProjectActions :many
SELECT DISTINCT rp.action
FROM project_members pm
         JOIN role_permissions rp ON rp.role_id = pm.role_id
         JOIN roles r ON r.id = pm.role_id AND r.deleted_at IS NULL
WHERE pm.project_id = $1 AND pm.user_id = $2;

-- name: ListActivePlacementsWithAction :many
SELECT pm.project_id
FROM project_members pm
         JOIN role_permissions rp ON rp.role_id = pm.role_id
         JOIN roles r ON r.id = pm.role_id AND r.deleted_at IS NULL
WHERE pm.organization_id = $1 AND pm.user_id = $2 AND rp.action = $3;
