-- Initial schema. Matches the GORM models in internal/{user,token,subscription,
-- models,campaign}; keep them in sync when changing either side.

BEGIN;

CREATE TABLE users (
    id          bigserial PRIMARY KEY,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz,
    email       text NOT NULL,
    picture_url text,
    oauth_id    text NOT NULL,
    first_name  text NOT NULL,
    last_name   text NOT NULL
);
CREATE UNIQUE INDEX idx_users_email ON users (email);
CREATE INDEX idx_users_deleted_at ON users (deleted_at);

-- Google OAuth tokens, encrypted by the application (AES-GCM)
CREATE TABLE tokens (
    id         bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    access     text NOT NULL,
    refresh    text NOT NULL,
    expiry     timestamptz NOT NULL
);
CREATE UNIQUE INDEX idx_tokens_user_id ON tokens (user_id);
CREATE INDEX idx_tokens_deleted_at ON tokens (deleted_at);

CREATE TABLE subscriptions (
    id                      bigserial PRIMARY KEY,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    deleted_at              timestamptz,
    plan                    text NOT NULL,
    is_active               boolean DEFAULT true,
    auto_renew              boolean DEFAULT false,
    started_at              timestamptz NOT NULL,
    end_at                  timestamptz NOT NULL,
    canceled_at             timestamptz,
    failed_payment_attempts bigint DEFAULT 0,
    user_id                 bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE
);
CREATE INDEX idx_subscriptions_user_id ON subscriptions (user_id);
CREATE INDEX idx_subscriptions_deleted_at ON subscriptions (deleted_at);

-- Payments are kept for accounting: no cascade from users/subscriptions
CREATE TABLE payments (
    id                  bigserial PRIMARY KEY,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    deleted_at          timestamptz,
    user_id             bigint NOT NULL REFERENCES users (id),
    subscription_id     bigint NOT NULL REFERENCES subscriptions (id),
    external_payment_id text,
    amount              numeric(10, 2),
    currency            text DEFAULT 'RUB',
    status              text DEFAULT 'pending',
    payment_method      text,
    description         text,
    paid_at             timestamptz
);
CREATE UNIQUE INDEX idx_payments_external_payment_id ON payments (external_payment_id);
CREATE INDEX idx_payments_user_id ON payments (user_id);
CREATE INDEX idx_payments_deleted_at ON payments (deleted_at);

CREATE TABLE campaigns (
    id           bigserial PRIMARY KEY,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz,
    user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    sender_name  text,
    subject      text NOT NULL,
    body         text NOT NULL,
    status       varchar(16) NOT NULL,
    pause_reason varchar(32),
    scheduled_at timestamptz NOT NULL,
    started_at   timestamptz,
    finished_at  timestamptz
);
CREATE INDEX idx_campaigns_user_id ON campaigns (user_id);
CREATE INDEX idx_campaigns_status ON campaigns (status);
CREATE INDEX idx_campaigns_scheduled_at ON campaigns (scheduled_at);
CREATE INDEX idx_campaigns_deleted_at ON campaigns (deleted_at);

-- One row per recipient; Postgres is the source of truth for sending state
CREATE TABLE recipients (
    id               bigserial PRIMARY KEY,
    campaign_id      bigint NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    user_id          bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email            text NOT NULL,
    status           varchar(16) NOT NULL,
    attempts         bigint NOT NULL DEFAULT 0,
    error            text,
    gmail_message_id text,
    gmail_thread_id  text,
    sent_at          timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_recipient_campaign_email ON recipients (campaign_id, email);
CREATE INDEX idx_recipient_campaign_status ON recipients (campaign_id, status);
CREATE INDEX idx_recipient_user_status ON recipients (user_id, status);

CREATE TABLE attachments (
    id          bigserial PRIMARY KEY,
    campaign_id bigint NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    filename    text NOT NULL,
    mime_type   text NOT NULL DEFAULT 'application/octet-stream',
    size        bigint NOT NULL,
    content     bytea NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_attachments_campaign_id ON attachments (campaign_id);

COMMIT;
