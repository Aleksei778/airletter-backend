BEGIN;

-- per-sender stop list: an address that unsubscribed from a user's emails
-- never gets another one from that user
CREATE TABLE unsubscribes (
    id          bigserial PRIMARY KEY,
    user_id     bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email       text NOT NULL,
    campaign_id bigint REFERENCES campaigns (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_unsubscribes_user_email ON unsubscribes (user_id, email);

COMMIT;
