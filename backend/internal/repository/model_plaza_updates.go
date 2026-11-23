package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Each lateral lookup reads only the latest indexed terminal/sample for one
// public model in one group. Notification checks never compute aggregates.
const plazaMetricsVersionQuery = `WITH scope AS (
 SELECT DISTINCT catalog_id,platform,model,group_id FROM jsonb_to_recordset($1::jsonb)
 AS x(catalog_id text,platform text,model text,group_id bigint)
)
SELECT s.catalog_id,p.created_at,p.id::text,t.finished_at,t.id,n.finished_at,n.id FROM scope s
LEFT JOIN LATERAL(SELECT created_at,id FROM sub2api_relay_performance_samples
 WHERE group_id=s.group_id AND platform=s.platform AND model=s.model AND created_at >= $2
 ORDER BY created_at DESC,id DESC LIMIT 1) p ON TRUE
LEFT JOIN LATERAL(SELECT finished_at,id FROM sub2api_generation_tasks
 WHERE group_id=s.group_id AND platform=s.platform AND model=s.model AND finished_at >= $2 AND status IN('succeeded','failed')
   AND error_code NOT IN('request_processing_rejected','request_processing_failed','canceled_before_submit')
 ORDER BY finished_at DESC,id DESC LIMIT 1) t ON TRUE
LEFT JOIN LATERAL(SELECT finished_at,id FROM sub2api_native_video_tasks
 WHERE group_id=s.group_id AND platform=s.platform AND model=s.model AND finished_at >= $2 AND status IN('succeeded','failed')
 ORDER BY finished_at DESC,id DESC LIMIT 1) n ON TRUE
ORDER BY s.catalog_id`

func (r *modelPlazaMetricsRepository) ReadModelPerformanceVersion(ctx context.Context, scopes []service.PlazaMetricsScope) (string, error) {
	if len(scopes) == 0 || len(scopes) > 24 {
		return "", errors.New("notification scope requires 1-24 models")
	}
	var groupID int64
	for _, scope := range scopes {
		if len(scope.GroupIDs) != 1 || scope.GroupIDs[0] <= 0 {
			return "", errors.New("notification scope requires one group")
		}
		if groupID != 0 && groupID != scope.GroupIDs[0] {
			return "", errors.New("notification scopes must share a group")
		}
		groupID = scope.GroupIDs[0]
	}
	encoded, err := encodePlazaMetricsScope(scopes)
	if err != nil {
		return "", err
	}
	rows, err := r.db.QueryContext(ctx, plazaMetricsVersionQuery, encoded, time.Now().Add(-24*time.Hour))
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	for rows.Next() {
		var id string
		var p, t, n sql.NullTime
		var pi, ti, ni sql.NullString
		if err := rows.Scan(&id, &p, &pi, &t, &ti, &n, &ni); err != nil {
			return "", err
		}
		if err := encoder.Encode([]any{id, p, pi, t, ti, n, ni}); err != nil {
			return "", err
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
