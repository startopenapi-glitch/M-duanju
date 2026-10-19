-- 邮箱验证码登录：一次性、10 分钟有效、按邮箱冷却
CREATE TABLE IF NOT EXISTS login_codes (
  id         varchar(32)  PRIMARY KEY DEFAULT app_gen_id(),
  email      varchar(255) NOT NULL,
  code_hash  varchar(128) NOT NULL,
  expires_at timestamptz  NOT NULL,
  attempts   int          NOT NULL DEFAULT 0,
  used_at    timestamptz,
  created_at timestamptz  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_login_codes_email ON login_codes (email, created_at DESC);
