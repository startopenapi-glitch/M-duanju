-- 身份与挑战消费：旧会话与旧无 pepper 挑战需重新获取。
ALTER TABLE users ADD COLUMN IF NOT EXISTS session_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_last_step BIGINT NOT NULL DEFAULT -1;
CREATE OR REPLACE FUNCTION bump_auth_session_version() RETURNS trigger AS $$
BEGIN
  IF NEW.password_hash IS DISTINCT FROM OLD.password_hash
    OR NEW.role IS DISTINCT FROM OLD.role OR NEW.status IS DISTINCT FROM OLD.status
    OR NEW.totp_enabled IS DISTINCT FROM OLD.totp_enabled
    OR NEW.totp_secret IS DISTINCT FROM OLD.totp_secret THEN
    NEW.session_version := OLD.session_version + 1;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS users_auth_session_version ON users;
CREATE TRIGGER users_auth_session_version BEFORE UPDATE ON users
  FOR EACH ROW EXECUTE PROCEDURE bump_auth_session_version();

-- 不猜测或重写历史邮箱归属。规范化碰撞由应用拒绝，待人工核对。
CREATE INDEX IF NOT EXISTS idx_users_normalized_email ON users(lower(btrim(email)));
UPDATE login_codes SET expires_at=LEAST(expires_at,now()) WHERE code_hash NOT LIKE 'v1:%';

CREATE TABLE IF NOT EXISTS auth_rate_limits (
  key TEXT PRIMARY KEY,
  window_started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  attempts INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS auth_code_sends (
  email VARCHAR(255) PRIMARY KEY,
  challenge_id VARCHAR(32) NOT NULL,
  last_started_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_login_codes_expiry ON login_codes(expires_at);
