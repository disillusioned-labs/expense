-- +goose Up
-- PROYEKSI read-only (keputusan D1): mirror "siapa member aktif" untuk validasi
-- rule, cek membership saat decide, dan dropdown approver - dibangun murni dari
-- event identity yang sudah diterbitkan (organization.created, invitation.accepted,
-- member.role_updated/removed/left, organization.deleted). Bukan sumber kebenaran;
-- identity tetap sumbernya. Idempotensi consumer per last_event_id.
CREATE TABLE organization_members
(
    organization_id UUID        NOT NULL,
    user_id         UUID        NOT NULL,
    role            VARCHAR(20) NOT NULL,
    member_status   VARCHAR(20) NOT NULL DEFAULT 'active', -- active | removed
    last_event_id   UUID        NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (organization_id, user_id),

    CONSTRAINT ck_proj_members_status
        CHECK (member_status IN ('active', 'removed'))
);

CREATE INDEX ix_proj_members_user_active
    ON organization_members (user_id) WHERE member_status = 'active';

CREATE TABLE organizations
(
    id            UUID         PRIMARY KEY,
    name          VARCHAR(255) NOT NULL,
    deleted_at    TIMESTAMPTZ  NULL, -- event organization.deleted → frozen: tolak tulisan baru
    last_event_id UUID         NOT NULL,
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS organizations;
DROP TABLE IF EXISTS organization_members;
