BEGIN;

ALTER TABLE users DROP CONSTRAINT users_email_present;

ALTER TABLE users ADD COLUMN phone text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_users_phone ON users (phone) WHERE phone <> '';
ALTER TABLE users ADD CONSTRAINT users_login_present CHECK (email <> '' OR phone <> '');

COMMIT;
