BEGIN;

-- campaigns sent on the trial carry a "Sent with Airletter" footer in the
-- language of the sender's interface
ALTER TABLE campaigns
    ADD COLUMN branded boolean NOT NULL DEFAULT false,
    ADD COLUMN locale varchar(2) NOT NULL DEFAULT 'ru' CHECK (locale IN ('ru', 'en'));

COMMIT;
