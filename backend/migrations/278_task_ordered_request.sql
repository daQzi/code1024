-- JSONB discards object member order. Keep the immutable decoder text for
-- newly accepted tasks so json-order@1 also survives polling and restarts.
-- Old rows deliberately remain NULL: their original order cannot be recovered.
ALTER TABLE sub2api_generation_tasks
 ADD COLUMN IF NOT EXISTS request_text TEXT
 CHECK (request_text IS NULL OR
        (octet_length(request_text)<=1048576 AND request_text::jsonb=request));
