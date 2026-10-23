-- +goose Up
CREATE TABLE package_downloads (
 pack_id text PRIMARY KEY REFERENCES library_packs(id) ON DELETE CASCADE,
 downloads bigint NOT NULL DEFAULT 0 CHECK(downloads>=0)
);
CREATE TABLE package_votes (
 pack_id text NOT NULL REFERENCES library_packs(id) ON DELETE CASCADE,
 user_id text NOT NULL REFERENCES account_users(id) ON DELETE CASCADE,
 rating smallint CHECK(rating BETWEEN 1 AND 5),
 liked boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(pack_id,user_id)
);
CREATE INDEX package_votes_user ON package_votes(user_id);
-- +goose Down
DROP TABLE package_votes;
DROP TABLE package_downloads;
