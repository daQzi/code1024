package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type modelPlazaMetricsRepository struct{ db *sql.DB }

func NewModelPlazaMetricsRepository(db *sql.DB) service.ModelPlazaMetricsRepository {
	return &modelPlazaMetricsRepository{db: db}
}

// The database computes percentiles over source samples for each grouping;
// hourly percentiles or per-group averages are never averaged together.
const plazaMetricsSamples = `
WITH scope AS (
 SELECT DISTINCT catalog_id,platform,model,group_id FROM jsonb_to_recordset($3::jsonb)
 AS x(catalog_id text,platform text,model text,group_id bigint)
), successes AS (
 SELECT DISTINCT ON (scope.catalog_id, ul.group_id, ul.api_key_id, COALESCE(NULLIF(ul.request_id,''), 'usage:' || ul.id::text))
   scope.catalog_id, ul.group_id, ul.api_key_id, ul.request_id, ul.created_at,
   ul.duration_ms::float8 AS duration_ms, ul.first_token_ms::float8 AS first_token_ms,
   ul.output_tokens::bigint AS output_tokens, TRUE AS success
 FROM usage_logs ul JOIN accounts a ON a.id = ul.account_id
 JOIN scope ON scope.group_id=ul.group_id AND scope.platform=a.platform
   AND scope.model=COALESCE(NULLIF(ul.requested_model,''),ul.model)
 WHERE ul.created_at >= $1 AND ul.created_at < $2
   AND ul.actual_cost > 0
   AND COALESCE(ul.video_duration_seconds,0) = 0
   AND COALESCE(ul.billing_mode,'') <> 'video'
 ORDER BY scope.catalog_id, ul.group_id, ul.api_key_id, COALESCE(NULLIF(ul.request_id,''), 'usage:' || ul.id::text), ul.created_at DESC
), failures AS (
 SELECT DISTINCT ON (scope.catalog_id, e.group_id, e.api_key_id, COALESCE(NULLIF(e.request_id,''), 'error:' || e.id::text))
   scope.catalog_id, e.group_id, e.api_key_id, e.request_id, e.created_at,
   NULL::float8 AS duration_ms, NULL::float8 AS first_token_ms,
   0::bigint AS output_tokens, FALSE AS success
 FROM ops_error_logs e JOIN scope ON scope.group_id=e.group_id AND scope.platform=e.platform
   AND scope.model=COALESCE(NULLIF(e.requested_model,''),e.model)
 WHERE e.created_at >= $1 AND e.created_at < $2
   AND COALESCE(e.status_code,0) >= 400
   AND NOT COALESCE(e.is_business_limited,FALSE) AND NOT COALESCE(e.is_count_tokens,FALSE)
   AND COALESCE(e.inbound_endpoint,'') NOT LIKE '%video%'
   AND COALESCE(e.request_path,'') NOT LIKE '%video%'
   AND COALESCE(e.request_path,'') NOT LIKE '%contents/generations/tasks%'
   AND NOT EXISTS (SELECT 1 FROM successes s WHERE s.catalog_id=scope.catalog_id AND s.group_id = e.group_id
     AND s.api_key_id = e.api_key_id AND NULLIF(s.request_id,'') = NULLIF(e.request_id,''))
 ORDER BY scope.catalog_id, e.group_id, e.api_key_id, COALESCE(NULLIF(e.request_id,''), 'error:' || e.id::text), e.created_at DESC
), samples AS (
 SELECT * FROM successes UNION ALL SELECT * FROM failures
 UNION ALL
 SELECT scope.catalog_id,t.group_id,t.api_key_id,t.id,t.finished_at,
   EXTRACT(EPOCH FROM(t.finished_at-t.created_at))*1000.0,
   NULL::float8,0::bigint,t.status='succeeded' AND t.delivery_status='ready'
 FROM sub2api_generation_tasks t JOIN scope ON scope.group_id=t.group_id AND scope.platform=t.platform AND scope.model=t.model
 WHERE t.finished_at >= $1 AND t.finished_at < $2 AND t.status IN('succeeded','failed')
), buckets AS (
 SELECT *, date_trunc('hour', created_at) AS bucket FROM samples
)`

const plazaMetricsQuery = plazaMetricsSamples + `
SELECT CASE WHEN GROUPING(group_id) = 1 THEN 0 ELSE group_id END,
 CASE WHEN GROUPING(bucket) = 1 THEN NULL ELSE bucket END,
 COUNT(*), COUNT(*) FILTER (WHERE success),
 AVG(duration_ms) FILTER (WHERE success AND duration_ms >= 0),
 percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE success AND duration_ms >= 0),
 AVG(first_token_ms) FILTER (WHERE success AND first_token_ms >= 0),
 SUM(output_tokens) FILTER (WHERE success AND output_tokens > 0 AND duration_ms > 0) * 1000.0 /
 NULLIF(SUM(duration_ms) FILTER (WHERE success AND output_tokens > 0 AND duration_ms > 0),0)
FROM buckets
GROUP BY GROUPING SETS ((group_id,bucket),(group_id),(bucket),())
ORDER BY 1,2 NULLS FIRST`

const plazaMetricsSummariesQuery = plazaMetricsSamples + `
SELECT catalog_id,COUNT(*),COUNT(*) FILTER(WHERE success),
 AVG(duration_ms) FILTER(WHERE success AND duration_ms>=0),
 percentile_cont(0.95) WITHIN GROUP(ORDER BY duration_ms) FILTER(WHERE success AND duration_ms>=0),
 AVG(first_token_ms) FILTER(WHERE success AND first_token_ms>=0),
 SUM(output_tokens) FILTER(WHERE success AND output_tokens>0 AND duration_ms>0)*1000.0 /
 NULLIF(SUM(duration_ms) FILTER(WHERE success AND output_tokens>0 AND duration_ms>0),0)
FROM buckets GROUP BY catalog_id`

func encodePlazaMetricsScope(scopes []service.PlazaMetricsScope) ([]byte, error) {
	type row struct {
		ID       string `json:"catalog_id"`
		Platform string `json:"platform"`
		Model    string `json:"model"`
		GroupID  int64  `json:"group_id"`
	}
	if len(scopes) > 100 {
		return nil, fmt.Errorf("too many model metric scopes")
	}
	rows := make([]row, 0)
	for _, scope := range scopes {
		if scope.Model == "" || scope.Platform == "" || len(scope.GroupIDs) > 1000 || len(rows)+len(scope.GroupIDs) > 10000 {
			return nil, fmt.Errorf("invalid model metrics scope")
		}
		for _, group := range scope.GroupIDs {
			rows = append(rows, row{ID: scope.ID, Platform: scope.Platform, Model: scope.Model, GroupID: group})
		}
	}
	return json.Marshal(rows)
}

func (r *modelPlazaMetricsRepository) ReadModelSummaries(ctx context.Context, scopes []service.PlazaMetricsScope, start, end time.Time) (map[string]service.PlazaMetricsPoint, error) {
	result := make(map[string]service.PlazaMetricsPoint)
	if len(scopes) == 0 {
		return result, nil
	}
	if !end.After(start) || end.Sub(start) > 24*time.Hour {
		return nil, fmt.Errorf("invalid model metrics window")
	}
	scope, err := encodePlazaMetricsScope(scopes)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, plazaMetricsSummariesQuery, start, end, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var p service.PlazaMetricsPoint
		if err := rows.Scan(&id, &p.Requests, &p.Successes, &p.AverageMs, &p.P95Ms, &p.FirstTokenMs, &p.TokensPerSecond); err != nil {
			return nil, err
		}
		result[id] = p
	}
	return result, rows.Err()
}

func (r *modelPlazaMetricsRepository) ReadModelMetrics(ctx context.Context, model, platform string, groups []int64, start, end time.Time) ([]service.PlazaMetricsPoint, error) {
	if len(groups) == 0 {
		return []service.PlazaMetricsPoint{}, nil
	}
	if len(groups) > 1000 || model == "" || platform == "" || !end.After(start) || end.Sub(start) > 24*time.Hour {
		return nil, fmt.Errorf("invalid model metrics scope")
	}
	scope, err := encodePlazaMetricsScope([]service.PlazaMetricsScope{{Model: model, Platform: platform, GroupIDs: groups}})
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, plazaMetricsQuery, start, end, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]service.PlazaMetricsPoint, 0)
	for rows.Next() {
		var p service.PlazaMetricsPoint
		var bucket sql.NullTime
		if err := rows.Scan(&p.GroupID, &bucket, &p.Requests, &p.Successes, &p.AverageMs, &p.P95Ms, &p.FirstTokenMs, &p.TokensPerSecond); err != nil {
			return nil, err
		}
		if bucket.Valid {
			value := bucket.Time.UTC()
			p.Bucket = &value
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
