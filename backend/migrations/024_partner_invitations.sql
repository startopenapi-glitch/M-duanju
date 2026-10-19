-- 代理邀请：管理员按邮箱发出一次性邀请，收件人登录/注册后接受。
-- 不改变价格、积分、分佣与结算规则；本迁移只新增邀请状态，不触碰财务表。
CREATE TABLE IF NOT EXISTS partner_invitations (
  id                   VARCHAR(32) PRIMARY KEY,
  email                VARCHAR(255) NOT NULL,
  name                 VARCHAR(80) NOT NULL,
  intro                VARCHAR(80) NOT NULL DEFAULT '',
  wechat               VARCHAR(64) NOT NULL DEFAULT '',
  token_hash           CHAR(64) NOT NULL,
  status               VARCHAR(16) NOT NULL DEFAULT 'pending',
  invited_by           VARCHAR(32) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  accepted_user_id     VARCHAR(32) REFERENCES users(id) ON DELETE RESTRICT,
  accepted_partner_id  VARCHAR(32) REFERENCES partners(id) ON DELETE RESTRICT,
  expires_at           TIMESTAMPTZ NOT NULL,
  send_count           INTEGER NOT NULL DEFAULT 1,
  last_sent_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  accepted_at          TIMESTAMPTZ,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT partner_invitations_status_chk CHECK (status IN ('pending','accepted','revoked')),
  CONSTRAINT partner_invitations_email_chk CHECK (email = lower(btrim(email)) AND position('@' in email) > 1),
  CONSTRAINT partner_invitations_name_chk CHECK (length(btrim(name)) > 0)
);
-- 同邮箱最多一条待接受邀请；已接受/撤销后可重新邀请（历史保留）。
CREATE UNIQUE INDEX IF NOT EXISTS uniq_partner_invite_pending_email
  ON partner_invitations(email) WHERE status='pending';
CREATE UNIQUE INDEX IF NOT EXISTS uniq_partner_invite_token
  ON partner_invitations(token_hash);
CREATE INDEX IF NOT EXISTS idx_partner_invite_status
  ON partner_invitations(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_partner_invite_email
  ON partner_invitations(email, created_at DESC);
