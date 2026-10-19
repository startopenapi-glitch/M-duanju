-- 为所有表补齐 UUID 默认值（PG10 无 gen_random_uuid()）
-- 用 md5(随机) 模拟，长度 32 位十六进制，满足 VARCHAR(64)

CREATE OR REPLACE FUNCTION app_gen_id() RETURNS text AS $$
BEGIN
  RETURN md5(random()::text || clock_timestamp()::text);
END;
$$ LANGUAGE plpgsql VOLATILE;

ALTER TABLE users              ALTER COLUMN id SET DEFAULT app_gen_id();
ALTER TABLE projects           ALTER COLUMN id SET DEFAULT app_gen_id();
ALTER TABLE characters         ALTER COLUMN id SET DEFAULT app_gen_id();
ALTER TABLE scenes             ALTER COLUMN id SET DEFAULT app_gen_id();
ALTER TABLE shots              ALTER COLUMN id SET DEFAULT app_gen_id();
ALTER TABLE shot_frames        ALTER COLUMN id SET DEFAULT app_gen_id();
ALTER TABLE audit_logs         ALTER COLUMN id SET DEFAULT app_gen_id();
ALTER TABLE credit_transactions ALTER COLUMN id SET DEFAULT app_gen_id();
ALTER TABLE recharge_orders    ALTER COLUMN id SET DEFAULT app_gen_id();
