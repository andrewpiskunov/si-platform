-- +goose Up
ALTER TABLE uploads ADD COLUMN purpose text NOT NULL DEFAULT 'siq' CHECK (purpose IN ('siq','media'));
ALTER TABLE uploads ADD COLUMN media_kind text NOT NULL DEFAULT '' CHECK (media_kind IN ('','image','audio','video'));
ALTER TABLE uploads ADD COLUMN result_asset jsonb;
ALTER TABLE uploads ADD CONSTRAINT uploads_media_purpose CHECK (
    (purpose='siq' AND media_kind='' AND result_asset IS NULL)
    OR (purpose='media' AND media_kind<>'' AND size<=104857600)
);
CREATE TABLE library_media_receipts (
    pack_id text NOT NULL,
    upload_id text NOT NULL REFERENCES uploads(id),
    revision integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(pack_id,upload_id),
    FOREIGN KEY(pack_id,revision) REFERENCES library_revisions(pack_id,revision)
);

-- +goose Down
DROP TABLE library_media_receipts;
ALTER TABLE uploads DROP CONSTRAINT uploads_media_purpose;
ALTER TABLE uploads DROP COLUMN result_asset;
ALTER TABLE uploads DROP COLUMN media_kind;
ALTER TABLE uploads DROP COLUMN purpose;
