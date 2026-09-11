-- +goose Up
CREATE TABLE project_members
(
    organization_id UUID        NOT NULL,
    project_id      UUID        NOT NULL REFERENCES projects(id),
    user_id         UUID        NOT NULL,
    role_id         UUID        NOT NULL REFERENCES roles(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX ix_project_members_user
    ON project_members (organization_id, user_id);

CREATE INDEX ix_project_members_role ON project_members (role_id);

CREATE TRIGGER trg_project_members_updated_at
    BEFORE UPDATE ON project_members
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS project_members;
