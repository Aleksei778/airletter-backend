BEGIN;

-- pause in seconds the sender chose between two emails of the campaign;
-- 0 (campaigns created before) means the server default
ALTER TABLE campaigns
    ADD COLUMN send_interval_seconds integer NOT NULL DEFAULT 0 CHECK (send_interval_seconds >= 0);

COMMIT;
