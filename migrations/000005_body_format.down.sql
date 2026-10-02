BEGIN;

ALTER TABLE attachments DROP COLUMN content_id;
ALTER TABLE campaigns DROP COLUMN body_format;

COMMIT;
