-- +goose Up
-- Append-only ditegakkan mesin, bukan niat baik: user aplikasi tidak bisa
-- mengubah atau menghapus riwayat keputusan. Kalau aturan ini dilanggar
-- sekali saja, seluruh nilai auditnya hilang.
REVOKE UPDATE, DELETE ON approval_decisions FROM expense_app;

-- +goose Down
GRANT UPDATE, DELETE ON approval_decisions TO expense_app;
