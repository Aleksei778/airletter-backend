-- Sign-in by email only: drop the phone login.

BEGIN;

ALTER TABLE users DROP CONSTRAINT users_login_present;
DROP INDEX idx_users_phone;
ALTER TABLE users DROP COLUMN phone;

-- fails if phone-only users exist: they need an email first
ALTER TABLE users ADD CONSTRAINT users_email_present CHECK (email <> '');

COMMIT;
