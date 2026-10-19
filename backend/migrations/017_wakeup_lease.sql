-- R08：outbox 消费者租约。旧消费者即使跑完，也不能覆盖新消费者对同一条唤醒的处理结果。
ALTER TABLE resume_wakeups ADD COLUMN IF NOT EXISTS lease_token BIGINT NOT NULL DEFAULT 0;
