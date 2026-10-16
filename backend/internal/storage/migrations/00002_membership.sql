-- +goose Up
ALTER TABLE users
    ADD COLUMN is_member BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN joined_at TIMESTAMPTZ;

-- Everyone who used the bot before invitations existed stays in the family.
UPDATE users SET is_member = true, joined_at = created_at;

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE settings;
ALTER TABLE users DROP COLUMN joined_at, DROP COLUMN is_member;
