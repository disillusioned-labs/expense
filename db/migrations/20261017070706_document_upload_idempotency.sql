-- +goose Up
ALTER TABLE documents
    ADD COLUMN idempotency_key UUID NULL,
    ADD COLUMN upload_complete BOOLEAN NOT NULL DEFAULT TRUE;

CREATE UNIQUE INDEX ux_documents_upload_idempotency
    ON documents (project_id, uploaded_by, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS ux_documents_upload_idempotency;

ALTER TABLE documents
    DROP COLUMN IF EXISTS upload_complete,
    DROP COLUMN IF EXISTS idempotency_key;
