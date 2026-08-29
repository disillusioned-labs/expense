-- +goose Up
-- Semua step di-INSERT sekaligus saat submit; step berikutnya diaktifkan dengan
-- mengisi activated_at, bukan INSERT baru. TIDAK ADA kolom status: diturunkan
-- (CANCELLED / DECIDED / WAITING / PENDING) dari activated_at, cancelled_at,
-- dan keberadaan baris di approval_decisions.
CREATE TABLE approvals
(
    id             UUID         PRIMARY KEY DEFAULT uuidv7(),
    transaction_id UUID         NOT NULL REFERENCES transactions(id),
    batch_id       UUID         NULL REFERENCES approval_batches(id),

    -- SNAPSHOT approver, dibekukan saat submit
    approver_id    UUID         NOT NULL,
    approver_name  VARCHAR(255) NOT NULL,
    approver_email VARCHAR(255) NOT NULL,
    approver_role  VARCHAR(20)  NOT NULL,

    step           SMALLINT     NOT NULL,
    activated_at   TIMESTAMPTZ  NULL, -- NULL = belum giliran
    cancelled_at   TIMESTAMPTZ  NULL, -- NULL = tidak dibatalkan
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Sabuk pengaman kedua setelah constraint approval_rules: satu orang tidak
-- boleh punya dua penugasan di transaksi yang sama.
CREATE UNIQUE INDEX ux_approvals_tx_approver
    ON approvals (transaction_id, approver_id);

CREATE INDEX ix_approvals_transaction
    ON approvals (transaction_id, step);

-- Query "tugas saya": approval aktif, belum diputuskan, tidak dibatalkan.
CREATE INDEX ix_approvals_pending_for_user
    ON approvals (approver_id)
    WHERE activated_at IS NOT NULL AND cancelled_at IS NULL;

CREATE INDEX ix_approvals_batch
    ON approvals (batch_id) WHERE batch_id IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS approvals;
