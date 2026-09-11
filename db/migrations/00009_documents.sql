-- +goose Up
CREATE TABLE documents
(
    id               UUID         PRIMARY KEY DEFAULT uuidv7(),
    transaction_id   UUID         NOT NULL REFERENCES transactions(id),
    storage_path     TEXT         NOT NULL,
    file_name        VARCHAR(255) NOT NULL,
    file_size        BIGINT       NOT NULL,
    mime_type        VARCHAR(100) NOT NULL,
    file_hash        VARCHAR(64)  NULL,

    ocr_status       VARCHAR(20)  NOT NULL DEFAULT 'pending',
    ocr_data         JSONB        NULL,
    ocr_processed_at TIMESTAMPTZ  NULL,

    uploaded_by      UUID         NOT NULL,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ  NULL,

    CONSTRAINT ck_documents_ocr_status
        CHECK (ocr_status IN ('pending', 'processing', 'completed', 'needs_review', 'failed'))
);

CREATE INDEX ix_documents_transaction
    ON documents (transaction_id) WHERE deleted_at IS NULL;

CREATE INDEX ix_documents_hash
    ON documents (file_hash) WHERE file_hash IS NOT NULL AND deleted_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS documents;
