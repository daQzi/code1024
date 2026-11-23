-- Preserve task, pricing and audit facts when the owner deletes delivery.
ALTER TABLE sub2api_generation_tasks
    ADD COLUMN IF NOT EXISTS public_deleted_at TIMESTAMPTZ;
