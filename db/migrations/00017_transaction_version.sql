-- +goose Up
ALTER TABLE transactions
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1,
    ADD CONSTRAINT ck_transactions_version CHECK (version > 0);

-- +goose StatementBegin
CREATE FUNCTION bump_transaction_version() RETURNS TRIGGER AS $$
BEGIN
    NEW.version := OLD.version + 1;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_transactions_bump_version
    BEFORE UPDATE ON transactions
    FOR EACH ROW EXECUTE FUNCTION bump_transaction_version();

-- +goose Down
DROP TRIGGER IF EXISTS trg_transactions_bump_version ON transactions;
DROP FUNCTION IF EXISTS bump_transaction_version();
ALTER TABLE transactions
    DROP CONSTRAINT IF EXISTS ck_transactions_version,
    DROP COLUMN IF EXISTS version;
