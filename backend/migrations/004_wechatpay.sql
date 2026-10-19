-- 微信支付 Native 扫码：订单补充字段
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS out_trade_no  VARCHAR(64);
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS code_url      TEXT;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS transaction_id VARCHAR(64);
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS paid_at      TIMESTAMPTZ;
ALTER TABLE recharge_orders ADD COLUMN IF NOT EXISTS pay_mode     VARCHAR(16) NOT NULL DEFAULT 'manual';

CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_out_trade_no
  ON recharge_orders(out_trade_no) WHERE out_trade_no IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_orders_txn ON recharge_orders(transaction_id) WHERE transaction_id IS NOT NULL;

-- 支付回调幂等：同一 transaction_id 只处理一次
CREATE TABLE IF NOT EXISTS pay_notify_logs (
  id             VARCHAR(64) PRIMARY KEY DEFAULT app_gen_id(),
  out_trade_no   VARCHAR(64) NOT NULL,
  transaction_id VARCHAR(64),
  event_type     VARCHAR(32),
  raw_body       TEXT,
  handled        BOOLEAN NOT NULL DEFAULT false,
  note           TEXT,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_notify_logs_txn
  ON pay_notify_logs(transaction_id) WHERE transaction_id IS NOT NULL;
