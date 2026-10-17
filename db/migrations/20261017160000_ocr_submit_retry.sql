-- +goose Up
ALTER TABLE documents
    ADD COLUMN ocr_submit_attempted_at TIMESTAMPTZ NULL;

CREATE INDEX ix_documents_ocr_submit_retry
    ON documents (created_at, ocr_submit_attempted_at)
    WHERE deleted_at IS NULL
      AND upload_complete
      AND ocr_status = 'pending'
      AND ocr_document_id IS NULL;

-- +goose Down
DROP INDEX IF EXISTS ix_documents_ocr_submit_retry;

ALTER TABLE documents
    DROP COLUMN IF EXISTS ocr_submit_attempted_at;
