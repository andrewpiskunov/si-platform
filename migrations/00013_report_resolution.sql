-- +goose Up
ALTER TABLE library_reports
 ADD COLUMN resolution text NOT NULL DEFAULT '',
 ADD COLUMN resolution_reason text NOT NULL DEFAULT '',
 ADD COLUMN resolved_by text REFERENCES account_users(id),
 ADD COLUMN resolved_at timestamptz;
UPDATE library_reports SET resolution='legacy' WHERE status='resolved';
ALTER TABLE library_reports ADD CONSTRAINT library_reports_resolution_check CHECK (
 (status='open' AND resolution='' AND resolution_reason='' AND resolved_by IS NULL AND resolved_at IS NULL)
 OR (status='resolved' AND resolution='legacy' AND resolution_reason='' AND resolved_by IS NULL AND resolved_at IS NULL)
 OR (status='resolved' AND resolution IN ('decision','resolved','dismissed') AND resolved_by IS NOT NULL AND resolved_at IS NOT NULL
     AND char_length(resolution_reason)<=2000 AND octet_length(resolution_reason)<=8000
     AND (resolution='decision' OR length(btrim(resolution_reason, E' \t\n\r'))>0))
);
CREATE INDEX library_reports_status_id ON library_reports(status,id);
CREATE INDEX library_decisions_pack_id ON library_decisions(pack_id,id);

-- +goose Down
DROP INDEX library_decisions_pack_id;
DROP INDEX library_reports_status_id;
ALTER TABLE library_reports DROP CONSTRAINT library_reports_resolution_check;
ALTER TABLE library_reports DROP COLUMN resolved_at, DROP COLUMN resolved_by, DROP COLUMN resolution_reason, DROP COLUMN resolution;
