-- +goose Up
CREATE TABLE approvals
(
    id             UUID         PRIMARY KEY DEFAULT uuidv7(),
    transaction_id UUID         NOT NULL REFERENCES transactions(id),

    approver_id    UUID         NOT NULL,
    approver_name  VARCHAR(255) NOT NULL,
    approver_email VARCHAR(255) NOT NULL,
    approver_role  VARCHAR(20)  NOT NULL,

    step           SMALLINT     NOT NULL,
    activated_at   TIMESTAMPTZ  NULL, -- NULL = belum giliran
    cancelled_at   TIMESTAMPTZ  NULL, -- NULL = tidak dibatalkan
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX ux_approvals_tx_approver
    ON approvals (transaction_id, approver_id);

CREATE INDEX ix_approvals_transaction
    ON approvals (transaction_id, step);

CREATE INDEX ix_approvals_pending_for_user
    ON approvals (approver_id)
    WHERE activated_at IS NOT NULL AND cancelled_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS approvals;
