-- One durable intent per order, committed before any outbound refund request.
CREATE TABLE IF NOT EXISTS refund_intents (
 id VARCHAR(64) PRIMARY KEY DEFAULT app_gen_id(),
 order_id VARCHAR(64) NOT NULL UNIQUE REFERENCES recharge_orders(id),
 refund_no VARCHAR(64) NOT NULL UNIQUE,
 status VARCHAR(16) NOT NULL CHECK(status IN ('submitting','processing','unknown','succeeded','failed')),
 amount_cents BIGINT NOT NULL CHECK(amount_cents > 0 OR legacy_unverified),
 credits_revoked BIGINT NOT NULL DEFAULT 0 CHECK(credits_revoked >= 0),
 credit_shortfall BIGINT NOT NULL DEFAULT 0 CHECK(credit_shortfall >= 0),
 reason TEXT NOT NULL,
 error TEXT NOT NULL DEFAULT '',
 provider_refund_id TEXT NOT NULL DEFAULT '',
 admin_id VARCHAR(64) REFERENCES users(id),
 legacy_unverified BOOLEAN NOT NULL DEFAULT false,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_refund_intents_pending ON refund_intents(status) WHERE status IN ('submitting','processing','unknown');
-- Historical local marks are NOT evidence of channel success. No rePOST or new debit.
INSERT INTO refund_intents(order_id,refund_no,status,amount_cents,reason,error,legacy_unverified,created_at)
 SELECT id,'LEGACY-' || md5(id),'unknown',refund_amount_cents,COALESCE(refund_reason,''),
 'Historical refund mark is unverified; manual reconciliation required',true,refunded_at
 FROM recharge_orders WHERE refunded_at IS NOT NULL
 ON CONFLICT(order_id) DO NOTHING;
