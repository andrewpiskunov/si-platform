-- +goose Up
ALTER TABLE account_users ADD COLUMN email text;
CREATE UNIQUE INDEX account_users_email_key ON account_users(email) WHERE email IS NOT NULL;
CREATE TABLE account_email_requests (
 token_hash text PRIMARY KEY,
 purpose text NOT NULL CHECK(purpose IN ('verify','reset')),
 email text NOT NULL,
 user_id text REFERENCES account_users(id),
 password_hash text,
 display_name text,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL
);
CREATE INDEX account_email_requests_email ON account_email_requests(email, created_at);
-- +goose Down
DROP TABLE account_email_requests;
DROP INDEX account_users_email_key;
ALTER TABLE account_users DROP COLUMN email;
