-- +goose Up
CREATE TABLE projects
(
    id              UUID         PRIMARY KEY DEFAULT uuidv7(),
    organization_id UUID         NOT NULL,
    name            VARCHAR(255) NOT NULL,
    created_by      UUID         NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ  NULL,
    archived_at     TIMESTAMPTZ  NULL
);

CREATE INDEX ix_projects_org
    ON projects (organization_id) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_projects_updated_at
    BEFORE UPDATE ON projects
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS projects;
