-- +goose Up
-- Extensions are database-wide, including for concurrent isolated test schemas.
SELECT pg_advisory_xact_lock(20261021, 10);
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

ALTER TABLE library_revisions
    ADD COLUMN search_text text COLLATE pg_catalog.pg_unicode_fast NOT NULL DEFAULT '',
    ADD COLUMN age_rating smallint CHECK (age_rating IN (0, 6, 12, 16, 18));

UPDATE library_revisions r SET
    search_text = concat_ws(E'\n', COALESCE(r.card->>'name',''), COALESCE(r.card->>'author',''), (
        SELECT string_agg(COALESCE(theme.value->>'name',''), E'\n' ORDER BY round.ordinality, theme.ordinality)
        FROM jsonb_array_elements(CASE WHEN jsonb_typeof(r.document->'rounds')='array' THEN r.document->'rounds' ELSE '[]'::jsonb END)
            WITH ORDINALITY AS round(value, ordinality)
        CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(round.value->'themes')='array' THEN round.value->'themes' ELSE '[]'::jsonb END)
            WITH ORDINALITY AS theme(value, ordinality)
    )),
    age_rating = CASE btrim(r.card->>'restriction', E' \t\r\n')
        WHEN '0+' THEN 0 WHEN '6+' THEN 6 WHEN '12+' THEN 12
        WHEN '16+' THEN 16 WHEN '18+' THEN 18 ELSE NULL END;

CREATE INDEX library_revisions_search ON library_revisions USING gin (search_text public.gin_trgm_ops);
CREATE INDEX library_revisions_age ON library_revisions (age_rating);

-- +goose Down
ALTER TABLE library_revisions DROP COLUMN search_text, DROP COLUMN age_rating;
-- pg_trgm may be used by other schemas/applications; never drop it here.
