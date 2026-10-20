-- +goose Up
CREATE TABLE room_packs (
 room_id text PRIMARY KEY REFERENCES rooms(id),
 owner_id text NOT NULL REFERENCES account_users(id),
 pack_id text,
 revision integer,
 title text NOT NULL,
 theme text NOT NULL CHECK (theme IN ('classic','pirate','anime')),
 demo_document jsonb,
 FOREIGN KEY(pack_id,revision) REFERENCES library_revisions(pack_id,revision),
 CHECK ((pack_id IS NOT NULL AND revision IS NOT NULL AND demo_document IS NULL) OR (pack_id IS NULL AND revision IS NULL AND demo_document IS NOT NULL))
);
-- +goose Down
DROP TABLE room_packs;
