-- +goose Up
-- TIDAK ADA kolom status: diturunkan dari anak-anaknya lewat view approval_batch_status (00013).
CREATE TABLE approval_batches
(
    id              UUID        PRIMARY KEY DEFAULT uuidv7(),
    organization_id UUID        NOT NULL,
    submitted_by    UUID        NOT NULL,
    submitted_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ix_approval_batches_org
    ON approval_batches (organization_id, submitted_at DESC);

-- +goose Down
DROP TABLE IF EXISTS approval_batches;
