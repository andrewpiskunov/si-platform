-- +goose Up
CREATE TABLE ai_admission (id integer PRIMARY KEY CHECK(id=1));
INSERT INTO ai_admission VALUES(1);
CREATE TABLE ai_quotes (
 owner_id text PRIMARY KEY REFERENCES account_users(id), id text NOT NULL UNIQUE,
 credential_id text NOT NULL, request jsonb NOT NULL, model text NOT NULL,
 input_rate bigint NOT NULL, output_rate bigint NOT NULL, max_output_tokens integer NOT NULL,
 input_tokens bigint, maximum_cost bigint, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '5 minutes',
 format_version text NOT NULL DEFAULT '1'
);
CREATE TABLE ai_monthly_usage (
 owner_id text NOT NULL REFERENCES account_users(id), month date NOT NULL,
 spent bigint NOT NULL DEFAULT 0 CHECK(spent>=0), reserved bigint NOT NULL DEFAULT 0 CHECK(reserved>=0),
 PRIMARY KEY(owner_id,month)
);
CREATE TABLE ai_jobs (
 id text PRIMARY KEY, owner_id text NOT NULL REFERENCES account_users(id), credential_id text NOT NULL DEFAULT '',
 status text NOT NULL CHECK(status IN ('queued','running','ready','failed','cancelled','uncertain')),
 request jsonb NOT NULL, result jsonb, model text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 completed_at timestamptz, started_at timestamptz, applied_pack_id text, applied_hash text NOT NULL DEFAULT '',
 quote_id text NOT NULL DEFAULT '', idempotency_key text NOT NULL, request_hash text NOT NULL,
 month date NOT NULL, input_rate bigint NOT NULL, output_rate bigint NOT NULL,
 input_tokens bigint NOT NULL,max_output_tokens integer NOT NULL,maximum_cost bigint NOT NULL CHECK(maximum_cost>=0),
 actual_cost bigint, cancel_requested boolean NOT NULL DEFAULT false,error text NOT NULL DEFAULT '',
 UNIQUE(owner_id,idempotency_key)
);
CREATE UNIQUE INDEX ai_jobs_one_active ON ai_jobs(owner_id) WHERE status IN ('queued','running');
CREATE INDEX ai_jobs_queue ON ai_jobs(created_at,id) WHERE status='queued';
CREATE INDEX ai_jobs_history ON ai_jobs(owner_id,created_at DESC,id DESC);
-- +goose Down
DROP TABLE ai_jobs;
DROP TABLE ai_monthly_usage;
DROP TABLE ai_quotes;
DROP TABLE ai_admission;
