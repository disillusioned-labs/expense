-- name: InsertTransactionItems :execrows
INSERT INTO transaction_items (transaction_id, description, quantity, amount)
SELECT $1,
       unnest(sqlc.arg('descriptions')::text[]),
       unnest(sqlc.arg('quantities')::int[]),
       unnest(sqlc.arg('amounts')::bigint[]);

-- name: ListTransactionItems :many
SELECT id, transaction_id, description, quantity, amount, created_at, updated_at
FROM transaction_items
WHERE transaction_id = $1
ORDER BY created_at, id;

-- name: DeleteTransactionItems :execrows
DELETE FROM transaction_items WHERE transaction_id = $1;
