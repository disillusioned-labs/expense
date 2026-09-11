-- +goose Up
CREATE TABLE role_permissions
(
    role_id UUID        NOT NULL REFERENCES roles(id),
    action  VARCHAR(50) NOT NULL,

    PRIMARY KEY (role_id, action)
);

CREATE INDEX ix_role_permissions_action ON role_permissions (action);

-- +goose Down
DROP TABLE IF EXISTS role_permissions;
