-- Sign-in by email or phone + password; Google becomes an integration
-- (sending via Gmail), not a way to sign in.

BEGIN;

-- email and phone are both optional, but at least one is required;
-- empty string means "not set" so the unique indexes skip it
ALTER TABLE users ALTER COLUMN email SET DEFAULT '';
DROP INDEX idx_users_email;
CREATE UNIQUE INDEX idx_users_email ON users (email) WHERE email <> '';

ALTER TABLE users ADD COLUMN phone text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_users_phone ON users (phone) WHERE phone <> '';

ALTER TABLE users ADD COLUMN password_hash text NOT NULL DEFAULT '';
ALTER TABLE users ADD CONSTRAINT users_login_present CHECK (email <> '' OR phone <> '');

ALTER TABLE users ALTER COLUMN first_name SET DEFAULT '';
ALTER TABLE users ALTER COLUMN last_name SET DEFAULT '';

-- the Google account moves to the connected-integration record
ALTER TABLE tokens ADD COLUMN google_sub text NOT NULL DEFAULT '';
ALTER TABLE tokens ADD COLUMN google_email text NOT NULL DEFAULT '';
UPDATE tokens t SET google_sub = u.oauth_id, google_email = u.email FROM users u WHERE u.id = t.user_id;
ALTER TABLE users DROP COLUMN oauth_id;

COMMIT;
