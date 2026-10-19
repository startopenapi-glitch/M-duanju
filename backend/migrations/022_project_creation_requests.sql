-- Durable creation identity; deletion leaves a tombstone instead of recreating
-- a deleted project on a delayed browser retry. No price or credit changes.
CREATE TABLE IF NOT EXISTS project_creation_requests (
 user_id VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 request_key VARCHAR(64) NOT NULL,
 input_hash VARCHAR(64) NOT NULL,
 project_id VARCHAR(64) REFERENCES projects(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY (user_id, request_key)
);
