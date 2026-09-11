-- +goose Up
CREATE TABLE roles
(
    id              UUID         PRIMARY KEY DEFAULT uuidv7(),
    project_id      UUID         NOT NULL,
    name            VARCHAR(100) NOT NULL,
    is_system       BOOLEAN      NOT NULL DEFAULT false,
    created_by      UUID         NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ  NULL
);

CREATE UNIQUE INDEX ux_roles_project_name
    ON roles (project_id, name) WHERE deleted_at IS NULL;

CREATE INDEX ix_roles_project ON roles (project_id) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_roles_updated_at
    BEFORE UPDATE ON roles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS roles;
