-- name: CreateDocument :one
INSERT INTO documents (project_id, transaction_id, storage_path, file_name, file_size,
                       mime_type, file_hash, uploaded_by, uploaded_by_name, uploaded_by_email, uploaded_by_role)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING
    id, project_id, transaction_id, storage_path, file_name, file_size, mime_type, file_hash,
    ocr_status, ocr_data, ocr_processed_at, uploaded_by, uploaded_by_name, uploaded_by_email,
    uploaded_by_role, created_at, deleted_at;

-- name: GetDocument :one
SELECT d.id, d.project_id, d.transaction_id, d.storage_path, d.file_name, d.file_size,
       d.mime_type, d.file_hash, d.ocr_status, d.ocr_data, d.ocr_processed_at,
       d.uploaded_by, d.uploaded_by_name, d.uploaded_by_email, d.uploaded_by_role,
       d.created_at, d.deleted_at
FROM documents d
         JOIN projects p ON p.id = d.project_id
WHERE d.id = $1 AND p.organization_id = $2 AND d.deleted_at IS NULL AND d.upload_complete;

-- name: ReserveDocumentUpload :one
INSERT INTO documents (id, project_id, transaction_id, storage_path, file_name, file_size,
                       mime_type, file_hash, uploaded_by, uploaded_by_name, uploaded_by_email,
                       uploaded_by_role, idempotency_key, upload_complete)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, false)
ON CONFLICT (project_id, uploaded_by, idempotency_key)
    WHERE idempotency_key IS NOT NULL DO NOTHING
RETURNING id;

-- name: GetDocumentUploadByKey :one
SELECT id, project_id, transaction_id, storage_path, file_name, file_size, mime_type,
       file_hash, ocr_status, ocr_data, ocr_processed_at, uploaded_by, uploaded_by_name,
       uploaded_by_email, uploaded_by_role, created_at, deleted_at, idempotency_key, upload_complete
FROM documents
WHERE project_id = $1 AND uploaded_by = $2 AND idempotency_key = $3;

-- Upload completion locks before emitting the audit event. Retries may write
-- the same bytes to the reserved object key, but only one completes the row.
-- name: LockDocumentUpload :one
SELECT id, project_id, transaction_id, storage_path, file_name, file_size, mime_type,
       file_hash, ocr_status, ocr_data, ocr_processed_at, uploaded_by, uploaded_by_name,
       uploaded_by_email, uploaded_by_role, created_at, deleted_at, idempotency_key, upload_complete
FROM documents
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: CompleteDocumentUpload :execrows
UPDATE documents
SET upload_complete = true
WHERE id = $1 AND upload_complete = false AND deleted_at IS NULL;

-- name: ListDocumentsByProject :many
SELECT id, project_id, transaction_id, storage_path, file_name, file_size, mime_type, file_hash,
       ocr_status, ocr_data, ocr_processed_at, uploaded_by, uploaded_by_name, uploaded_by_email,
       uploaded_by_role, created_at, deleted_at
FROM documents
WHERE project_id = $1 AND deleted_at IS NULL AND upload_complete
ORDER BY created_at ASC;

-- name: ListDocumentsByTransaction :many
SELECT id, project_id, transaction_id, storage_path, file_name, file_size, mime_type, file_hash,
       ocr_status, ocr_data, ocr_processed_at, uploaded_by, uploaded_by_name, uploaded_by_email,
       uploaded_by_role, created_at, deleted_at
FROM documents
WHERE transaction_id = $1 AND deleted_at IS NULL AND upload_complete
ORDER BY created_at ASC;

-- name: SoftDeleteDocument :execrows
UPDATE documents
SET deleted_at = now()
WHERE id = $1 AND deleted_at IS NULL AND upload_complete;

-- ClaimDocumentForOCR is the consumer's idempotency guard: exactly one
-- delivery of document.processed may drive a document out of 'pending', so a
-- redelivered event claims zero rows and is skipped instead of creating a
-- second draft transaction.
-- name: ClaimDocumentForOCR :execrows
UPDATE documents
SET ocr_status = 'processing'
WHERE id = $1 AND ocr_status = 'pending' AND deleted_at IS NULL AND upload_complete;

-- This row lock serializes OCR attachment/draft creation with document delete.
-- OCR creation locks document then inserts a transaction; delete locks a known
-- transaction first, then the document, and locks a newly linked draft second.
-- name: GetDocumentForProcessing :one
SELECT d.id, d.project_id, d.transaction_id, d.storage_path, d.file_name, d.file_size,
       d.mime_type, d.file_hash, d.ocr_status, d.ocr_data, d.ocr_processed_at,
       d.uploaded_by, d.uploaded_by_name, d.uploaded_by_email, d.uploaded_by_role,
       d.created_at, d.deleted_at,
       p.organization_id
FROM documents d
         JOIN projects p ON p.id = d.project_id
WHERE d.id = $1 AND d.deleted_at IS NULL AND d.upload_complete
FOR UPDATE OF d;

-- name: CompleteDocumentOCR :execrows
UPDATE documents
SET ocr_status = $2, ocr_data = $3, ocr_processed_at = now()
WHERE id = $1 AND ocr_status = 'processing' AND deleted_at IS NULL AND upload_complete;

-- name: AttachDocumentToTransaction :execrows
UPDATE documents
SET transaction_id = $2, ocr_status = $3, ocr_data = $4, ocr_processed_at = now()
WHERE id = $1 AND ocr_status = 'processing' AND deleted_at IS NULL AND upload_complete;

-- name: FailDocumentOCR :execrows
UPDATE documents
SET ocr_status = 'failed', ocr_processed_at = now()
WHERE id = $1 AND ocr_status = 'processing' AND deleted_at IS NULL AND upload_complete;

-- name: SetOcrDocumentID :execrows
-- Stores the ocr job id returned at submit time, so the reconciliation sweep
-- can ask the gateway about documents whose routed event never arrived.
UPDATE documents
SET ocr_document_id = $2
WHERE id = $1
  AND ocr_document_id IS NULL
  AND upload_complete
  AND deleted_at IS NULL;

-- name: ListStaleOCRDocuments :many
-- Documents still waiting on OCR past the sweep threshold that have a job id
-- to ask about; documents without a job ID are handled by ClaimStaleOCRSubmissions.
SELECT id, ocr_document_id
FROM documents
WHERE deleted_at IS NULL
  AND ocr_status IN ('pending', 'processing')
  AND ocr_document_id IS NOT NULL
  AND upload_complete
  AND created_at < $1
ORDER BY created_at
LIMIT $2;

-- name: ClaimStaleOCRSubmissions :many
-- Claim a bounded batch before calling the gateway. The timestamp provides
-- retry backoff and prevents one permanently failing row from starving later
-- documents when multiple worker instances run.
WITH candidates AS (
    SELECT d.id
    FROM documents d
    WHERE d.deleted_at IS NULL
      AND d.ocr_status = 'pending'
      AND d.ocr_document_id IS NULL
      AND d.upload_complete
      AND d.created_at < $1
      AND (d.ocr_submit_attempted_at IS NULL OR d.ocr_submit_attempted_at < $2)
    ORDER BY COALESCE(d.ocr_submit_attempted_at, d.created_at), d.created_at
    LIMIT $3
    FOR UPDATE OF d SKIP LOCKED
)
UPDATE documents d
SET ocr_submit_attempted_at = now()
FROM candidates c, projects p
WHERE d.id = c.id AND p.id = d.project_id
RETURNING d.id, p.organization_id, d.uploaded_by, d.storage_path, d.file_size, d.mime_type;
