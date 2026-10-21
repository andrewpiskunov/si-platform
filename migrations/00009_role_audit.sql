-- +goose Up
ALTER TABLE account_audit DROP CONSTRAINT account_audit_action_check;
ALTER TABLE account_audit ADD CONSTRAINT account_audit_action_check CHECK (
    action IN ('registered','login','logout','password_changed','deleted','token_created','token_revoked',
               'role_author','role_moderator','role_admin')
);

-- +goose Down
-- Preserve historical role audit entries during rollback; new writes are
-- restricted to the old action set without deleting the operator history.
ALTER TABLE account_audit DROP CONSTRAINT account_audit_action_check;
ALTER TABLE account_audit ADD CONSTRAINT account_audit_action_check CHECK (
    action IN ('registered','login','logout','password_changed','deleted','token_created','token_revoked')
) NOT VALID;
