-- Payments through YooKassa and Stripe. A payment is created before the
-- provider's checkout and gets its subscription when it succeeds.

BEGIN;

ALTER TABLE payments ADD COLUMN provider text NOT NULL DEFAULT '';
ALTER TABLE payments ADD COLUMN plan text NOT NULL DEFAULT '';
ALTER TABLE payments ADD COLUMN period text NOT NULL DEFAULT '';
ALTER TABLE payments ALTER COLUMN subscription_id DROP NOT NULL;
ALTER TABLE payments ALTER COLUMN currency DROP DEFAULT;

-- external ids are unique per provider, not globally
DROP INDEX idx_payments_external_payment_id;
CREATE UNIQUE INDEX idx_payments_provider_external_id ON payments (provider, external_payment_id)
    WHERE external_payment_id IS NOT NULL;

COMMIT;
