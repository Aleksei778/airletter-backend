BEGIN;

ALTER TABLE users ADD COLUMN oauth_id text NOT NULL DEFAULT '';
UPDATE users u SET oauth_id = t.google_sub FROM tokens t WHERE t.user_id = u.id;
ALTER TABLE tokens DROP COLUMN google_email;
ALTER TABLE tokens DROP COLUMN google_sub;

ALTER TABLE users ALTER COLUMN last_name DROP DEFAULT;
ALTER TABLE users ALTER COLUMN first_name DROP DEFAULT;

ALTER TABLE users DROP CONSTRAINT users_login_present;
ALTER TABLE users DROP COLUMN password_hash;
DROP INDEX idx_users_phone;
ALTER TABLE users DROP COLUMN phone;

-- fails if phone-only users exist: they have no email to keep
DROP INDEX idx_users_email;
CREATE UNIQUE INDEX idx_users_email ON users (email);
ALTER TABLE users ALTER COLUMN email DROP DEFAULT;

COMMIT;
