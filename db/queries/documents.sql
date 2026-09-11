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
WHERE d.id = $1 AND p.organization_id = $2 AND d.deleted_at IS NULL;

-- name: ListDocumentsByProject :many
SELECT id, project_id, transaction_id, storage_path, file_name, file_size, mime_type, file_hash,
       ocr_status, ocr_data, ocr_processed_at, uploaded_by, uploaded_by_name, uploaded_by_email,
       uploaded_by_role, created_at, deleted_at
FROM documents
WHERE project_id = $1 AND deleted_at IS NULL
ORDER BY created_at ASC;

-- name: ListDocumentsByTransaction :many
SELECT id, project_id, transaction_id, storage_path, file_name, file_size, mime_type, file_hash,
       ocr_status, ocr_data, ocr_processed_at, uploaded_by, uploaded_by_name, uploaded_by_email,
       uploaded_by_role, created_at, deleted_at
FROM documents
WHERE transaction_id = $1 AND deleted_at IS NULL
ORDER BY created_at ASC;

-- name: SoftDeleteDocument :execrows
UPDATE documents
SET deleted_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- ClaimDocumentForOCR is the consumer's idempotency guard: exactly one
-- delivery of document.processed may drive a document out of 'pending', so a
-- redelivered event claims zero rows and is skipped instead of creating a
-- second draft transaction.
-- name: ClaimDocumentForOCR :execrows
UPDATE documents
SET ocr_status = 'processing'
WHERE id = $1 AND ocr_status = 'pending' AND deleted_at IS NULL;

-- name: GetDocumentForProcessing :one
SELECT d.id, d.project_id, d.transaction_id, d.storage_path, d.file_name, d.file_size,
       d.mime_type, d.file_hash, d.ocr_status, d.ocr_data, d.ocr_processed_at,
       d.uploaded_by, d.uploaded_by_name, d.uploaded_by_email, d.uploaded_by_role,
       d.created_at, d.deleted_at,
       p.organization_id
FROM documents d
         JOIN projects p ON p.id = d.project_id
WHERE d.id = $1 AND d.deleted_at IS NULL;

-- name: CompleteDocumentOCR :execrows
UPDATE documents
SET ocr_status = $2, ocr_data = $3, ocr_processed_at = now()
WHERE id = $1 AND ocr_status = 'processing';

-- name: AttachDocumentToTransaction :execrows
UPDATE documents
SET transaction_id = $2, ocr_status = $3, ocr_data = $4, ocr_processed_at = now()
WHERE id = $1 AND ocr_status = 'processing';

-- name: FailDocumentOCR :execrows
UPDATE documents
SET ocr_status = 'failed', ocr_processed_at = now()
WHERE id = $1 AND ocr_status = 'processing';
