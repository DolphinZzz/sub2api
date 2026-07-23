ALTER TABLE studio_requests
    ADD COLUMN IF NOT EXISTS async_task_id VARCHAR(128);

CREATE INDEX IF NOT EXISTS studio_requests_async_task_idx
    ON studio_requests (user_id, async_task_id)
    WHERE async_task_id IS NOT NULL;
