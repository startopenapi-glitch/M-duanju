-- Fence late LLM responses and recover abandoned draft generation.
ALTER TABLE projects ADD COLUMN script_generation_token BIGINT NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN script_lease_until TIMESTAMPTZ;
