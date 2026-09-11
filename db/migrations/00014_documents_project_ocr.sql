-- +goose Up
-- Documents move to project level: a nota is uploaded against a project and
-- only receives its transaction once the OCR consumer auto-creates the draft.
-- The uploaded_* snapshot columns freeze who uploaded the file, because the
-- consumer runs without an actor and cannot join identity for name/email.
ALTER TABLE documents
    ADD COLUMN project_id UUID,
    ALTER COLUMN transaction_id DROP NOT NULL,
    ADD COLUMN uploaded_by_name VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN uploaded_by_email VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN uploaded_by_role VARCHAR(20) NOT NULL DEFAULT '';

UPDATE documents
SET project_id = t.project_id
FROM transactions t
WHERE documents.transaction_id = t.id;

ALTER TABLE documents
    ALTER COLUMN project_id SET NOT NULL,
    ADD CONSTRAINT fk_documents_project FOREIGN KEY (project_id) REFERENCES projects(id);

CREATE INDEX ix_documents_project
    ON documents (project_id) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS ix_documents_project;
ALTER TABLE documents
    DROP CONSTRAINT IF EXISTS fk_documents_project,
    DROP COLUMN IF EXISTS project_id,
    DROP COLUMN IF EXISTS uploaded_by_name,
    DROP COLUMN IF EXISTS uploaded_by_email,
    DROP COLUMN IF EXISTS uploaded_by_role;
-- Fails while project-level documents (transaction_id IS NULL) exist; delete
-- those rows first.
ALTER TABLE documents ALTER COLUMN transaction_id SET NOT NULL;
