package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *modelPlazaMetricsRepository) WriteRelayPerformance(ctx context.Context, samples []service.RelayPerformanceSample) error {
	if len(samples) == 0 {
		return nil
	}
	if len(samples) > 128 {
		return errors.New("too many relay performance samples")
	}
	for _, sample := range samples {
		if !sample.Valid() {
			return errors.New("invalid relay performance sample")
		}
	}
	payload, err := json.Marshal(samples)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO sub2api_relay_performance_samples
 (id,group_id,platform,model,success,duration_ms,first_token_ms,output_tokens,created_at)
 SELECT id,group_id,platform,model,success,duration_ms,first_token_ms,output_tokens,created_at
 FROM jsonb_to_recordset($1::jsonb) AS x(id uuid,group_id bigint,platform text,model text,success boolean,duration_ms bigint,first_token_ms bigint,output_tokens bigint,created_at timestamptz)
 ON CONFLICT(id) DO NOTHING`, payload)
	return err
}
func (r *modelPlazaMetricsRepository) CleanupRelayPerformance(ctx context.Context, before time.Time, limit int) error {
	if limit < 1 || limit > 5000 {
		return errors.New("invalid relay performance cleanup limit")
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM sub2api_relay_performance_samples WHERE id IN
 (SELECT id FROM sub2api_relay_performance_samples WHERE created_at<$1 ORDER BY created_at,id LIMIT $2)`, before, limit)
	return err
}
