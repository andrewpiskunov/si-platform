-- +goose Up
CREATE TABLE account_users (
    id text PRIMARY KEY,
    username text UNIQUE,
    display_name text NOT NULL,
    role text NOT NULL DEFAULT 'author' CHECK (role IN ('author', 'moderator', 'admin')),
    password_hash text,
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CHECK ((deleted_at IS NULL AND username IS NOT NULL AND username ~ '^[a-z0-9_]{3,32}$' AND password_hash IS NOT NULL)
        OR (deleted_at IS NOT NULL AND username IS NULL AND password_hash IS NULL))
);
CREATE TABLE account_credentials (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES account_users(id),
    token_hash text NOT NULL UNIQUE,
    kind text NOT NULL CHECK (kind IN ('session', 'api')),
    name text NOT NULL DEFAULT '',
    scopes text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CHECK (scopes <@ ARRAY['drafts:read', 'drafts:write', 'drafts:export', 'ai:generate']::text[]),
    CHECK ((kind = 'session' AND cardinality(scopes) = 0) OR (kind = 'api' AND cardinality(scopes) > 0))
);
CREATE INDEX account_credentials_user ON account_credentials(user_id);
CREATE TABLE account_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id text NOT NULL REFERENCES account_users(id),
    action text NOT NULL CHECK (action IN ('registered', 'login', 'logout', 'password_changed', 'deleted', 'token_created', 'token_revoked')),
    target_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX account_audit_user ON account_audit(user_id, id);

-- +goose Down
DROP TABLE account_audit;
DROP TABLE account_credentials;
DROP TABLE account_users;
