-- +goose Up
-- APPEND-ONLY: nol UPDATE, nol DELETE, selamanya. Ditegakkan migrasi 00014 (REVOKE).
-- Salah input? Tambah baris koreksi - sama seperti jurnal koreksi di akuntansi.
CREATE TABLE approval_decisions
(
    id               UUID         PRIMARY KEY DEFAULT uuidv7(),
    approval_id      UUID         NOT NULL REFERENCES approvals(id),

    -- SNAPSHOT pemutus. Bisa BEDA dari approver_id (delegasi, iterasi 2).
    decided_by       UUID         NOT NULL,
    decided_by_name  VARCHAR(255) NOT NULL,
    decided_by_email VARCHAR(255) NOT NULL,
    decided_by_role  VARCHAR(20)  NOT NULL,

    decision         VARCHAR(20)  NOT NULL,
    notes            TEXT         NULL, -- wajib saat decision = 'rejected' (ditegakkan di service)
    decided_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT ck_approval_decisions_decision
        CHECK (decision IN ('approved', 'rejected'))
);

CREATE INDEX ix_approval_decisions_approval
    ON approval_decisions (approval_id);

CREATE INDEX ix_approval_decisions_decider
    ON approval_decisions (decided_by, decided_at DESC);

-- +goose Down
DROP TABLE IF EXISTS approval_decisions;
