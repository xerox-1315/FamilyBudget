-- +goose Up
-- The name a member chose for the family budget; empty means "use the Telegram name".
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE users DROP COLUMN display_name;
