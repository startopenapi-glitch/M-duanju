-- 短剧生成平台 · 数据库结构
-- PostgreSQL 10+

-- ─────────── 用户 ───────────
CREATE TABLE IF NOT EXISTS users (
  id             VARCHAR(64) PRIMARY KEY ,
  email          VARCHAR(255) UNIQUE NOT NULL,
  password_hash  VARCHAR(255) NOT NULL,
  nickname       VARCHAR(64),
  role           VARCHAR(16)  NOT NULL DEFAULT 'user',   -- user / admin
  credits        BIGINT       NOT NULL DEFAULT 0,        -- 积分（1元 = 100积分）
  status         VARCHAR(16)  NOT NULL DEFAULT 'active', -- active / banned
  created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

-- ─────────── 项目（一次短片创作） ───────────
CREATE TABLE IF NOT EXISTS projects (
  id              VARCHAR(64) PRIMARY KEY ,
  user_id         VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title           VARCHAR(128) NOT NULL DEFAULT '未命名短片',
  idea            TEXT NOT NULL,
  style_config    JSONB NOT NULL DEFAULT '{}'::jsonb,
  aspect          VARCHAR(8)  NOT NULL DEFAULT '16:9',   -- 16:9 / 9:16
  total_seconds   INTEGER     NOT NULL DEFAULT 45,       -- 45 ~ 300
  status          VARCHAR(32) NOT NULL DEFAULT 'DRAFT',
  budget_credits  BIGINT      NOT NULL DEFAULT 3000,     -- 金额熔断（积分）
  spent_credits   BIGINT      NOT NULL DEFAULT 0,
  keyvisual_url   TEXT,
  final_url       TEXT,
  error_msg       TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_projects_user ON projects(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_projects_status ON projects(status);

-- ─────────── 角色卡 ───────────
CREATE TABLE IF NOT EXISTS characters (
  id            VARCHAR(64) PRIMARY KEY ,
  project_id    VARCHAR(64) NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  code          VARCHAR(16) NOT NULL,
  name          VARCHAR(64) NOT NULL,
  age_range     VARCHAR(32),
  appearance    TEXT NOT NULL DEFAULT '',
  wardrobe      TEXT NOT NULL DEFAULT '',
  persona       TEXT NOT NULL DEFAULT '',
  voice_profile TEXT NOT NULL DEFAULT '',
  sort_order    SMALLINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_characters_project ON characters(project_id);

-- ─────────── 场景 ───────────
CREATE TABLE IF NOT EXISTS scenes (
  id          VARCHAR(64) PRIMARY KEY ,
  project_id  VARCHAR(64) NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  code        VARCHAR(16) NOT NULL,
  name        VARCHAR(64) NOT NULL DEFAULT '',
  location    TEXT NOT NULL DEFAULT '',
  time_of_day VARCHAR(32) NOT NULL DEFAULT '',
  lighting    TEXT NOT NULL DEFAULT '',
  color_temp  VARCHAR(64) NOT NULL DEFAULT '',
  props       JSONB NOT NULL DEFAULT '[]'::jsonb
);
CREATE INDEX IF NOT EXISTS idx_scenes_project ON scenes(project_id);

-- ─────────── 分镜 ───────────
CREATE TABLE IF NOT EXISTS shots (
  id             VARCHAR(64) PRIMARY KEY ,
  project_id     VARCHAR(64) NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  scene_id       VARCHAR(64) REFERENCES scenes(id),
  shot_code      VARCHAR(16) NOT NULL,
  seq            SMALLINT NOT NULL,
  duration       REAL NOT NULL DEFAULT 5.0,
  shot_size      VARCHAR(32) NOT NULL DEFAULT '',
  camera_height  VARCHAR(32) NOT NULL DEFAULT '',
  camera_move    VARCHAR(32) NOT NULL DEFAULT '',
  visual_prompt  TEXT NOT NULL DEFAULT '',
  speaker_code   VARCHAR(16),
  dialogue       TEXT,
  soundscape     TEXT NOT NULL DEFAULT '',
  music_cue      TEXT NOT NULL DEFAULT '',
  status         VARCHAR(32) NOT NULL DEFAULT 'PENDING',
  retry_count    SMALLINT NOT NULL DEFAULT 0,
  upstream_id    VARCHAR(64),              -- 力萌 task id
  video_url      TEXT,
  duration_actual REAL,
  width          INTEGER,
  height         INTEGER,
  charge_micros  BIGINT,
  error_msg      TEXT,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(project_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_shots_project ON shots(project_id, seq);

-- ─────────── 分镜关键帧 ───────────
CREATE TABLE IF NOT EXISTS shot_frames (
  id         VARCHAR(64) PRIMARY KEY ,
  shot_id    VARCHAR(64) NOT NULL REFERENCES shots(id) ON DELETE CASCADE,
  project_id VARCHAR(64) NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  path       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_frames_shot ON shot_frames(shot_id);

-- ─────────── 操作日志（全链路可追溯） ───────────
CREATE TABLE IF NOT EXISTS audit_logs (
  id          VARCHAR(64) PRIMARY KEY ,
  user_id     VARCHAR(64) REFERENCES users(id) ON DELETE SET NULL,
  project_id  VARCHAR(64) REFERENCES projects(id) ON DELETE SET NULL,
  shot_id     VARCHAR(64) REFERENCES shots(id) ON DELETE SET NULL,
  action      VARCHAR(48) NOT NULL,
  from_status VARCHAR(32),
  to_status   VARCHAR(32),
  note        TEXT,
  ip          VARCHAR(64),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_logs_project ON audit_logs(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_logs_user ON audit_logs(user_id, created_at DESC);

-- ─────────── 积分流水 ───────────
CREATE TABLE IF NOT EXISTS credit_transactions (
  id           VARCHAR(64) PRIMARY KEY ,
  user_id      VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  amount       BIGINT NOT NULL,               -- 正=充值，负=消耗
  balance_after BIGINT NOT NULL,
  kind         VARCHAR(24) NOT NULL,          -- recharge / spend / refund / admin_adjust
  project_id   VARCHAR(64) REFERENCES projects(id) ON DELETE SET NULL,
  note         TEXT,
  operator_id  VARCHAR(64) REFERENCES users(id) ON DELETE SET NULL,  -- 管理员操作时记录
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tx_user ON credit_transactions(user_id, created_at DESC);

-- ─────────── 充值订单 ───────────
CREATE TABLE IF NOT EXISTS recharge_orders (
  id            VARCHAR(64) PRIMARY KEY ,
  user_id       VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  amount_yuan   NUMERIC(10,2) NOT NULL,
  credits       BIGINT NOT NULL,
  status        VARCHAR(16) NOT NULL DEFAULT 'pending',  -- pending/paid/rejected
  channel       VARCHAR(32),                            -- alipay/wechat/manual
  external_id   VARCHAR(128),
  admin_note    TEXT,
  handled_by    VARCHAR(64) REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  handled_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_orders_user ON recharge_orders(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_status ON recharge_orders(status, created_at DESC);
