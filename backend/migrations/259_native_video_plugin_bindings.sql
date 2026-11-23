-- Project supported native video routes from the account's immutable plugin
-- revision. This neither enables plugins nor moves accounts to newer source.
-- Keep in sync with pluginNativeVideoBinding. Existing tasks are untouched.
WITH bindings AS (
 SELECT v.id, jsonb_object_agg(paths.protocol, COALESCE(route->'models','[]'::jsonb)) AS routes
 FROM sub2api_js_plugin_versions v
 CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(v.meta->'routes')='array' THEN v.meta->'routes' ELSE '[]'::jsonb END) AS route
 JOIN (VALUES
   ('xai_video','/v1/videos/generations'),
   ('xai_video_edit','/v1/videos/edits'),
   ('xai_video_extension','/v1/videos/extensions'),
   ('ark_video','/api/v3/contents/generations/tasks')
 ) AS paths(protocol,path) ON route->>'path'=paths.path
 WHERE v.capabilities->>'task'='true'
   AND route->>'method'='POST' AND route->>'type'='submit'
   AND COALESCE(route->>'decode','')<>'' AND COALESCE(route->>'render','')<>''
   AND route->>'retainResult' IS DISTINCT FROM 'false'
 GROUP BY v.id
), updated AS (
 UPDATE accounts a
 SET credentials=jsonb_set(a.credentials,'{newapi_plugin_native_videos}',b.routes),updated_at=NOW()
 FROM bindings b
 WHERE a.type='apikey' AND a.deleted_at IS NULL
   AND a.credentials->>'newapi_plugin_version_id'=b.id::text
   AND a.credentials->'newapi_plugin_native_videos' IS DISTINCT FROM b.routes
 RETURNING a.id
)
INSERT INTO scheduler_outbox(event_type,account_id)
SELECT 'account_changed',id FROM updated;
