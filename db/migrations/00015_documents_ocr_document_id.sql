-- +goose Up
-- The reconciliation sweep needs the ocr job id: the gateway's GetDocument
-- answers by ocr document_id, and the routed Kafka event is only the primary
-- delivery path, not the only one.
ALTER TABLE documents
    ADD COLUMN ocr_document_id UUID NULL;

CREATE INDEX ix_documents_ocr_stale
    ON documents (ocr_status, created_at)
    WHERE deleted_at IS NULL AND ocr_status IN ('pending', 'processing');

-- +goose Down
DROP INDEX IF EXISTS ix_documents_ocr_stale;
ALTER TABLE documents
    DROP COLUMN IF EXISTS ocr_document_id;
