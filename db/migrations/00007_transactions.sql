-- +goose Up
CREATE TABLE transactions
(
    id                UUID         PRIMARY KEY DEFAULT uuidv7(),
    organization_id   UUID         NOT NULL,
    project_id        UUID         NOT NULL REFERENCES projects(id),
    status            VARCHAR(20)  NOT NULL DEFAULT 'draft',
    description       TEXT         NULL,
    total_amount      BIGINT       NOT NULL DEFAULT 0,
    currency          CHAR(3)      NOT NULL DEFAULT 'IDR',
    category          VARCHAR(50)  NULL,

    created_by        UUID         NOT NULL,
    created_by_name   VARCHAR(255) NOT NULL,
    created_by_email  VARCHAR(255) NOT NULL,
    created_by_role   VARCHAR(20)  NOT NULL,

    submitted_at      TIMESTAMPTZ  NULL,
    decided_at        TIMESTAMPTZ  NULL,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ  NULL,

    CONSTRAINT ck_transactions_status
        CHECK (status IN ('draft', 'pending_approval', 'approved', 'rejected')),
    CONSTRAINT ck_transactions_amount
        CHECK (total_amount >= 0),
    CONSTRAINT ck_transactions_delete_draft_only
        CHECK (deleted_at IS NULL OR status = 'draft')
);

CREATE INDEX ix_transactions_org_status
    ON transactions (organization_id, status) WHERE deleted_at IS NULL;

CREATE INDEX ix_transactions_project
    ON transactions (project_id) WHERE deleted_at IS NULL;

CREATE INDEX ix_transactions_created_by
    ON transactions (organization_id, created_by) WHERE deleted_at IS NULL;

CREATE INDEX ix_transactions_org_created
    ON transactions (organization_id, created_at DESC) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_transactions_updated_at
    BEFORE UPDATE ON transactions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS transactions;
