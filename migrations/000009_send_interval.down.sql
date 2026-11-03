BEGIN;

ALTER TABLE campaigns
    DROP COLUMN send_interval_seconds;

COMMIT;
