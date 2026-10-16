-- +goose Up
ALTER TABLE project_members
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1,
    ADD CONSTRAINT ck_project_members_version CHECK (version > 0);

-- +goose Down
ALTER TABLE project_members
    DROP CONSTRAINT ck_project_members_version,
    DROP COLUMN version;
