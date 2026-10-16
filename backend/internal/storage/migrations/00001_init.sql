-- +goose Up
CREATE TABLE users (
    telegram_id BIGINT PRIMARY KEY,
    username    TEXT NOT NULL DEFAULT '',
    first_name  TEXT NOT NULL DEFAULT '',
    last_name   TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TYPE expense_category AS ENUM (
    'food', 'transport', 'housing', 'entertainment', 'health', 'shopping', 'other'
);

CREATE TABLE expenses (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users (telegram_id) ON DELETE CASCADE,
    amount     BIGINT NOT NULL CHECK (amount > 0), -- kopecks
    category   expense_category NOT NULL,
    spent_at   DATE NOT NULL,
    comment    TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX expenses_user_spent_idx ON expenses (user_id, spent_at DESC, id DESC);
CREATE INDEX expenses_spent_idx ON expenses (spent_at DESC, id DESC);

-- +goose Down
DROP TABLE expenses;
DROP TYPE expense_category;
DROP TABLE users;
