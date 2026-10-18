-- 代理推广（一期：推广与客户归属，不含分佣/提现/多级）。
-- 所有金额与积分规则不变；本迁移只新增归属与审计结构，不触碰已有财务表。

CREATE TABLE IF NOT EXISTS partners (
  id            VARCHAR(32) PRIMARY KEY,
  user_id       VARCHAR(32) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  name          VARCHAR(80) NOT NULL,
  intro         VARCHAR(80) NOT NULL DEFAULT '',
  wechat        VARCHAR(64) NOT NULL DEFAULT '',
  code          VARCHAR(32) NOT NULL,
  status        VARCHAR(16) NOT NULL DEFAULT 'active',
  version       BIGINT NOT NULL DEFAULT 1,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT partners_status_chk CHECK (status IN ('active','suspended')),
  CONSTRAINT partners_name_chk CHECK (length(btrim(name)) > 0)
);
-- 同一账号最多一个合作方身份；推广码全局唯一且不复用
CREATE UNIQUE INDEX IF NOT EXISTS uniq_partners_user ON partners(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_partners_code ON partners(code);

-- 每个用户最多一条归属；改名不改历史身份（快照 partner_name/code 仅用于审计追溯）
CREATE TABLE IF NOT EXISTS partner_attributions (
  id            VARCHAR(32) PRIMARY KEY,
  user_id       VARCHAR(32) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  partner_id    VARCHAR(32) NOT NULL REFERENCES partners(id) ON DELETE RESTRICT,
  code          VARCHAR(32) NOT NULL,
  source        VARCHAR(16) NOT NULL DEFAULT 'link',
  attributed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT partner_attributions_source_chk CHECK (source IN ('link','test'))
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_attr_user ON partner_attributions(user_id);
CREATE INDEX IF NOT EXISTS idx_attr_partner ON partner_attributions(partner_id, attributed_at);

-- 每笔订单最多一份来源快照；下单时固化代理与当时状态，仅用于统计
CREATE TABLE IF NOT EXISTS partner_order_attributions (
  id                    VARCHAR(32) PRIMARY KEY,
  order_id              VARCHAR(32) NOT NULL REFERENCES recharge_orders(id) ON DELETE RESTRICT,
  partner_id            VARCHAR(32) NOT NULL REFERENCES partners(id) ON DELETE RESTRICT,
  partner_status_at_pay VARCHAR(16) NOT NULL DEFAULT 'active',
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_order_attr ON partner_order_attributions(order_id);
CREATE INDEX IF NOT EXISTS idx_order_attr_partner ON partner_order_attributions(partner_id, created_at);

-- 审计只追加，不级联删除
CREATE TABLE IF NOT EXISTS partner_audit_events (
  id          VARCHAR(32) PRIMARY KEY,
  partner_id  VARCHAR(32) NOT NULL REFERENCES partners(id) ON DELETE RESTRICT,
  actor_id    VARCHAR(32) NOT NULL,
  action      VARCHAR(32) NOT NULL,
  detail      JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_partner_audit ON partner_audit_events(partner_id, created_at DESC);
