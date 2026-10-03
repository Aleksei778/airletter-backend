BEGIN;

ALTER TABLE campaigns
    DROP COLUMN locale,
    DROP COLUMN branded;

COMMIT;
