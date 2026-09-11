-- +goose Up
CREATE TABLE transaction_items
(
    id             UUID        PRIMARY KEY DEFAULT uuidv7(),
    transaction_id UUID        NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    description    TEXT        NOT NULL,
    quantity       INT         NOT NULL DEFAULT 1,
    amount         BIGINT      NOT NULL,

    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT ck_transaction_items_quantity CHECK (quantity > 0),
    CONSTRAINT ck_transaction_items_amount CHECK (amount >= 0)
);

CREATE INDEX ix_transaction_items_transaction
    ON transaction_items (transaction_id);

CREATE TRIGGER trg_transaction_items_updated_at
    BEFORE UPDATE ON transaction_items
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementBegin
CREATE FUNCTION sync_transaction_total() RETURNS trigger AS $$
DECLARE
    tid UUID;
BEGIN
    tid := COALESCE(NEW.transaction_id, OLD.transaction_id);
    UPDATE transactions
    SET total_amount = COALESCE((SELECT SUM(amount) FROM transaction_items WHERE transaction_id = tid), 0)
    WHERE id = tid;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_transaction_items_sync_total
    AFTER INSERT OR UPDATE OR DELETE ON transaction_items
    FOR EACH ROW EXECUTE FUNCTION sync_transaction_total();

-- +goose Down
DROP FUNCTION IF EXISTS sync_transaction_total() CASCADE;
DROP TABLE IF EXISTS transaction_items;
