-- +goose Up
-- Status batch diturunkan dari anak-anaknya - tidak ada kolom status yang
-- perlu "didamaikan", karena memang tidak ada yang menulisnya.
CREATE VIEW approval_batch_status AS
SELECT
    b.id                AS batch_id,
    b.organization_id,
    CASE
        WHEN COUNT(*) FILTER (WHERE t.status = 'pending_approval') = 0
            THEN 'completed'
        WHEN COUNT(*) FILTER (WHERE t.status <> 'pending_approval') = 0
            THEN 'pending'
        ELSE 'partially_done'
    END                 AS status,
    COUNT(*)            AS total_transactions,
    COUNT(*) FILTER (WHERE t.status = 'approved')  AS approved_count,
    COUNT(*) FILTER (WHERE t.status = 'rejected')  AS rejected_count
FROM approval_batches b
JOIN approvals a    ON a.batch_id = b.id
JOIN transactions t ON t.id = a.transaction_id
GROUP BY b.id, b.organization_id;

-- +goose Down
DROP VIEW IF EXISTS approval_batch_status;
