-- 微信支付：按生产规格补齐字段（金额一律整数分）
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS amount_cents BIGINT;
UPDATE recharge_orders SET amount_cents = ROUND(amount_yuan * 100)::bigint WHERE amount_cents IS NULL;
ALTER TABLE recharge_orders ALTER COLUMN amount_cents SET DEFAULT 0;
ALTER TABLE recharge_orders ALTER COLUMN amount_cents SET NOT NULL;

ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS h5_url           TEXT;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS credited         BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS mock             BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS updated_at       TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS notify_count     INT NOT NULL DEFAULT 0;

-- status 扩展：pending → paid / closed / expired / failed，rejected 保留给人工驳回
COMMENT ON COLUMN recharge_orders.status IS 'pending/paid/closed/expired/failed/rejected';
COMMENT ON COLUMN recharge_orders.channel IS 'native/h5/manual';
COMMENT ON COLUMN recharge_orders.amount_cents IS '金额，整数分，永远不用浮点';

-- 对账与幂等索块
CREATE INDEX IF NOT EXISTS idx_orders_pending ON recharge_orders(status, updated_at)
  WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_orders_credited ON recharge_orders(id) WHERE credited = false;
