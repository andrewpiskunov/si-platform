-- +goose Up
-- Old quotes/jobs deliberately remain incompatible: their dispatch contract was not recorded.
ALTER TABLE ai_quotes ADD COLUMN provider_id text NOT NULL DEFAULT '';
ALTER TABLE ai_jobs ADD COLUMN provider_id text NOT NULL DEFAULT '';
ALTER TABLE ai_jobs ADD COLUMN format_version text NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE ai_jobs DROP COLUMN format_version;
ALTER TABLE ai_jobs DROP COLUMN provider_id;
ALTER TABLE ai_quotes DROP COLUMN provider_id;
