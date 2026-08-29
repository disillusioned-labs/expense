-- +goose Up
CREATE TABLE approval_rules
(
    id              UUID        PRIMARY KEY DEFAULT uuidv7(),
    organization_id UUID        NOT NULL, -- referensi logis
    project_id      UUID        NULL REFERENCES projects(id), -- NULL = aturan default organisasi
    approver_id     UUID        NOT NULL, -- referensi logis
    step            SMALLINT    NOT NULL DEFAULT 1,
    created_by      UUID        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ NULL,

    CONSTRAINT ck_approval_rules_step
        CHECK (step >= 1 AND step <= 10)
);

-- Satu orang hanya boleh muncul SEKALI per project, di step manapun.
-- `step` sengaja TIDAK masuk constraint.
CREATE UNIQUE INDEX ux_approval_rules_project_approver
    ON approval_rules (organization_id, project_id, approver_id)
    WHERE project_id IS NOT NULL AND deleted_at IS NULL;

-- Rule default (project_id NULL) butuh index terpisah: Postgres memperlakukan
-- NULL sebagai tidak sama di UNIQUE.
CREATE UNIQUE INDEX ux_approval_rules_default_approver
    ON approval_rules (organization_id, approver_id)
    WHERE project_id IS NULL AND deleted_at IS NULL;

CREATE INDEX ix_approval_rules_lookup
    ON approval_rules (organization_id, project_id, step)
    WHERE deleted_at IS NULL;

CREATE TRIGGER trg_approval_rules_updated_at
    BEFORE UPDATE ON approval_rules
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS approval_rules;
