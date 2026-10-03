BEGIN;

DROP INDEX idx_payments_provider_external_id;
CREATE UNIQUE INDEX idx_payments_external_payment_id ON payments (external_payment_id);

ALTER TABLE payments ALTER COLUMN currency SET DEFAULT 'RUB';
-- fails if unpaid payments without a subscription exist
ALTER TABLE payments ALTER COLUMN subscription_id SET NOT NULL;
ALTER TABLE payments DROP COLUMN period;
ALTER TABLE payments DROP COLUMN plan;
ALTER TABLE payments DROP COLUMN provider;

COMMIT;
