-- name: CreateTransaction :one
INSERT INTO transactions (organization_id, project_id, description, currency,
                          category, created_by, created_by_name, created_by_email, created_by_role)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING
    id, organization_id, project_id, status, description, total_amount, currency, category,
    created_by, created_by_name, created_by_email, created_by_role,
    submitted_at, decided_at, created_at, updated_at, deleted_at;

-- name: GetTransaction :one
SELECT id, organization_id, project_id, status, description, total_amount, currency, category,
       created_by, created_by_name, created_by_email, created_by_role,
       submitted_at, decided_at, created_at, updated_at, deleted_at
FROM transactions
WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL;

-- name: LockTransaction :one
SELECT id, organization_id, project_id, status, description, total_amount, currency, category,
       created_by, created_by_name, created_by_email, created_by_role,
       submitted_at, decided_at, created_at, updated_at, deleted_at
FROM transactions
WHERE id = $1
FOR UPDATE;

-- name: ListTransactions :many
SELECT t.id, t.organization_id, t.project_id, t.status, t.description, t.total_amount, t.currency,
       t.category, t.created_by, t.created_by_name, t.created_by_email, t.created_by_role,
       t.submitted_at, t.decided_at, t.created_at, t.updated_at,
       p.name AS project_name,
       (SELECT COUNT(*) FROM documents d WHERE d.transaction_id = t.id AND d.deleted_at IS NULL) AS document_count,
       (SELECT COUNT(*) FROM approvals a WHERE a.transaction_id = t.id)                          AS total_steps,
       (SELECT MIN(a.step) FROM approvals a
        WHERE a.transaction_id = t.id AND a.activated_at IS NOT NULL AND a.cancelled_at IS NULL
          AND NOT EXISTS (SELECT 1 FROM approval_decisions ad WHERE ad.approval_id = a.id))      AS current_step,
       (SELECT COALESCE(json_agg(json_build_object('id', w.approver_id, 'name', w.approver_name)), '[]'::json)
        FROM approvals w
        WHERE w.transaction_id = t.id AND w.activated_at IS NOT NULL AND w.cancelled_at IS NULL
          AND NOT EXISTS (SELECT 1 FROM approval_decisions ad WHERE ad.approval_id = w.id))      AS waiting_for
FROM transactions t
         JOIN projects p ON p.id = t.project_id
WHERE t.organization_id = $1 AND t.deleted_at IS NULL
  AND (sqlc.narg('project_ids')::uuid[] IS NULL OR t.project_id = ANY(sqlc.narg('project_ids')))
  AND (sqlc.narg('status')::text IS NULL OR t.status = sqlc.narg('status'))
  AND (sqlc.narg('project_id')::uuid IS NULL OR t.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('created_by')::uuid IS NULL OR t.created_by = sqlc.narg('created_by'))
  AND (sqlc.narg('category')::text IS NULL OR t.category = sqlc.narg('category'))
  AND (sqlc.narg('date_from')::timestamptz IS NULL OR t.created_at >= sqlc.narg('date_from'))
  AND (sqlc.narg('date_to')::timestamptz IS NULL OR t.created_at <= sqlc.narg('date_to'))
ORDER BY t.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: UpdateTransactionDraft :one
UPDATE transactions
SET description = $3, currency = $4, category = $5
WHERE id = $1 AND organization_id = $2 AND status = 'draft' AND deleted_at IS NULL RETURNING
    id, organization_id, project_id, status, description, total_amount, currency, category,
    created_by, created_by_name, created_by_email, created_by_role,
    submitted_at, decided_at, created_at, updated_at, deleted_at;

-- name: SoftDeleteTransaction :execrows
UPDATE transactions
SET deleted_at = now()
WHERE id = $1 AND organization_id = $2 AND status = 'draft' AND deleted_at IS NULL;

-- name: SubmitTransaction :execrows
UPDATE transactions
SET status = 'pending_approval', submitted_at = now()
WHERE id = $1 AND status = 'draft' AND deleted_at IS NULL;

-- name: ApproveTransaction :execrows
UPDATE transactions
SET status = 'approved', decided_at = now()
WHERE id = $1 AND status = 'pending_approval';

-- name: RejectTransaction :execrows
UPDATE transactions
SET status = 'rejected', decided_at = now()
WHERE id = $1 AND status = 'pending_approval';

-- name: CountTransactionDocuments :one
SELECT COUNT(*) FROM documents WHERE transaction_id = $1 AND deleted_at IS NULL;

