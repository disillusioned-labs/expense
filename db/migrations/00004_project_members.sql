-- +goose Up
-- project_id NOT NULL sengaja: tidak ada fallback NULL (fallback menggantikan seluruhnya
-- hanya berlaku untuk approval_rules). Deny-by-default: tidak ada baris berarti tidak punya akses.
CREATE TABLE project_members
(
    organization_id UUID        NOT NULL,
    project_id      UUID        NOT NULL REFERENCES projects(id),
    user_id         UUID        NOT NULL, -- referensi logis
    can_view        BOOLEAN     NOT NULL DEFAULT false,
    can_submit      BOOLEAN     NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX ix_project_members_user
    ON project_members (organization_id, user_id);

CREATE TRIGGER trg_project_members_updated_at
    BEFORE UPDATE ON project_members
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS project_members;
