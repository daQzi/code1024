package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRelayPerformanceLocalPostgresWebSocketCutover(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user, key, group, account int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash) VALUES('ws-metrics@example.invalid','fixture') RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('ws-metrics','openai') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'ws-metrics-key','fixture',$2) RETURNING id`, user, group).Scan(&key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials) VALUES('ws-metrics','openai','apikey','{}') RETURNING id`).Scan(&account))
	now := time.Now().UTC()
	_, err := db.Exec(`UPDATE sub2api_relay_metric_epoch SET started_at=$1,websocket_started_at=$2,media_http_started_at=$3`, now.Add(-2*time.Hour), now.Add(-time.Hour), now.Add(time.Hour))
	require.NoError(t, err)
	for _, row := range []struct {
		id          string
		created     time.Time
		requestType int
		ws          bool
		endpoint    string
	}{
		{"historical-ws", now.Add(-90 * time.Minute), 3, true, "/v1/responses"},
		{"post-cutover-partial-bill", now, 3, true, "/v1/responses"},
		{"legacy-ws-flag", now, 0, true, "/v1/responses"},
		{"alias-missing-endpoint", now, 3, true, ""},
		{"unmigrated-image", now, 1, false, "/v1/images/generations"},
	} {
		_, err := db.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,group_id,request_id,model,output_tokens,duration_ms,first_token_ms,actual_cost,inbound_endpoint,request_type,openai_ws_mode,created_at)
VALUES($1,$2,$3,$4,$5,'public-model',100,3000,1000,0,$6,$7,$8,$9)`, user, key, account, group, row.id, row.endpoint, row.requestType, row.ws, row.created)
		require.NoError(t, err)
	}
	// An old error log must not add a second failure for a sampled final turn.
	_, err = db.Exec(`INSERT INTO ops_error_logs(user_id,api_key_id,account_id,group_id,request_id,platform,model,error_phase,error_type,status_code,stream,request_type,inbound_endpoint,created_at)
VALUES($1,$2,$3,$4,'failed-turn','openai','public-model','upstream','server_error',502,true,3,'/v1/responses',$5)`, user, key, account, group, now)
	require.NoError(t, err)
	repo := NewModelPlazaMetricsRepository(db)
	writer := repo.(service.RelayPerformanceRepository)
	first := int64(1000)
	samples := []service.RelayPerformanceSample{
		{ID: "00000000-0000-4000-8000-000000000071", GroupID: group, Platform: "openai", Model: "public-model", Success: true, DurationMs: 5000, FirstTokenMs: &first, OutputTokens: 200, CreatedAt: now},
		{ID: "00000000-0000-4000-8000-000000000072", GroupID: group, Platform: "openai", Model: "public-model", Success: false, DurationMs: 1000, CreatedAt: now},
		{ID: "00000000-0000-4000-8000-000000000073", GroupID: group + 1, Platform: "openai", Model: "public-model", Success: true, DurationMs: 1, CreatedAt: now},
	}
	require.NoError(t, writer.WriteRelayPerformance(ctx, samples))
	require.NoError(t, writer.WriteRelayPerformance(ctx, samples))
	points, err := repo.ReadModelMetrics(ctx, "public-model", "openai", []int64{group}, now.Add(-3*time.Hour), now.Add(time.Minute))
	require.NoError(t, err)
	var total service.PlazaMetricsPoint
	for _, point := range points {
		if point.GroupID == 0 && point.Bucket == nil {
			total = point
		}
	}
	require.EqualValues(t, 4, total.Requests, "one historical WS + one untouched image + two independent final turns")
	require.EqualValues(t, 3, total.Successes)
	require.InDelta(t, 50, *total.TokensPerSecond, 0.001)
	var httpEpoch, wsEpoch, repeatedHTTP, repeatedWS time.Time
	require.NoError(t, db.QueryRow(`SELECT started_at,websocket_started_at FROM sub2api_relay_metric_epoch`).Scan(&httpEpoch, &wsEpoch))
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, db.QueryRow(`SELECT started_at,websocket_started_at FROM sub2api_relay_metric_epoch`).Scan(&repeatedHTTP, &repeatedWS))
	require.True(t, httpEpoch.Equal(repeatedHTTP))
	require.True(t, wsEpoch.Equal(repeatedWS))
	// Independent samples survive billing/ops retention operations.
	_, err = db.Exec(`DELETE FROM usage_logs WHERE created_at >= $1`, now.Add(-time.Hour))
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM ops_error_logs`)
	require.NoError(t, err)
	points, err = repo.ReadModelMetrics(ctx, "public-model", "openai", []int64{group}, now.Add(-time.Hour), now.Add(time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 2, points[0].Requests)
}
