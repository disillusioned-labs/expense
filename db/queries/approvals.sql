-- name: CreateApproval :exec
INSERT INTO approvals (transaction_id, approver_id, approver_name, approver_email, approver_role, step, activated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListApprovalsByTransaction :many
SELECT id, transaction_id, approver_id, approver_name, approver_email, approver_role,
       step, activated_at, cancelled_at, created_at
FROM approvals
WHERE transaction_id = $1
ORDER BY step ASC, created_at ASC;

-- name: ListDecisionsByTransaction :many
SELECT d.id, d.approval_id, d.decided_by, d.decided_by_name, d.decided_by_email, d.decided_by_role,
       d.decision, d.notes, d.decided_at
FROM approval_decisions d
         JOIN approvals a ON a.id = d.approval_id
WHERE a.transaction_id = $1
ORDER BY d.decided_at ASC;

-- name: GetApprovalForDecide :one
SELECT a.id, a.transaction_id, a.approver_id, a.approver_name, a.approver_email,
       a.approver_role, a.step, a.activated_at, a.cancelled_at, a.created_at,
       t.organization_id, t.status AS transaction_status,
       t.description, t.total_amount, t.currency, t.created_by,
       t.created_by_name, t.created_by_email
FROM approvals a
         JOIN transactions t ON t.id = a.transaction_id
WHERE a.id = $1
FOR UPDATE OF t;

-- name: ApprovalHasDecision :one
SELECT EXISTS (SELECT 1 FROM approval_decisions WHERE approval_id = $1) AS has_decision;

-- name: CreateApprovalDecision :one
INSERT INTO approval_decisions (approval_id, decided_by, decided_by_name, decided_by_email, decided_by_role, decision, notes)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING
    id, approval_id, decided_by, decided_by_name, decided_by_email, decided_by_role, decision, notes, decided_at;

-- name: CancelPendingApprovals :execrows
UPDATE approvals
SET cancelled_at = now()
WHERE transaction_id = $1 AND cancelled_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM approval_decisions d WHERE d.approval_id = approvals.id);

-- name: ListPendingForUser :many
SELECT a.id, a.transaction_id, a.approver_id, a.approver_name, a.approver_email,
       a.approver_role, a.step, a.activated_at, a.cancelled_at, a.created_at,
       t.organization_id, t.status AS transaction_status, t.description, t.total_amount, t.currency,
       t.category, t.created_by, t.created_by_name, t.created_by_email, t.created_by_role,
       t.submitted_at, t.project_id, p.name AS project_name,
       (SELECT COUNT(*) FROM documents d WHERE d.transaction_id = t.id AND d.deleted_at IS NULL) AS document_count
FROM approvals a
         JOIN transactions t ON t.id = a.transaction_id
         JOIN projects p ON p.id = t.project_id
WHERE a.approver_id = $1
  AND t.organization_id = $2
  AND a.activated_at IS NOT NULL
  AND a.cancelled_at IS NULL
  AND t.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM approval_decisions d WHERE d.approval_id = a.id)
ORDER BY a.activated_at ASC;

-- name: ActivateApprovalStep :execrows
UPDATE approvals
SET activated_at = now()
WHERE transaction_id = $1 AND step = $2 AND activated_at IS NULL AND cancelled_at IS NULL;

-- name: ListAgingTransactions :many
-- Pending transactions whose oldest active (undecided) approval step has been
-- waiting longer than the cutoff timestamp. One row per transaction.
SELECT t.id, t.description, t.total_amount, t.currency, t.project_id,
       p.name AS project_name, t.submitted_at,
       MIN(a.activated_at)::timestamptz AS waiting_since,
       COUNT(DISTINCT a.approver_id) AS pending_approver_count,
       string_agg(DISTINCT a.approver_name, ', ')::text AS pending_approver_names
FROM approvals a
         JOIN transactions t ON t.id = a.transaction_id
         JOIN projects p ON p.id = t.project_id
WHERE t.organization_id = $1
  AND t.status = 'pending_approval'
  AND t.deleted_at IS NULL
  AND a.activated_at IS NOT NULL
  AND a.cancelled_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM approval_decisions d WHERE d.approval_id = a.id)
  AND a.activated_at <= $2
GROUP BY t.id, p.name
ORDER BY waiting_since ASC
LIMIT $3 OFFSET $4;

-- name: ListStaleActiveApprovals :many
-- Active (undecided) approval steps waiting longer than the cutoff, across
-- all organizations - the reminder worker's feed.
SELECT a.id, a.step, a.activated_at, a.approver_id,
       t.organization_id, t.id AS transaction_id, t.description,
       t.total_amount, t.currency
FROM approvals a
         JOIN transactions t ON t.id = a.transaction_id
WHERE a.activated_at IS NOT NULL
  AND a.cancelled_at IS NULL
  AND t.status = 'pending_approval'
  AND t.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM approval_decisions d WHERE d.approval_id = a.id)
  AND a.activated_at <= $1
ORDER BY a.activated_at ASC
LIMIT $2;
