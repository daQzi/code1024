package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRelayPerformanceLocalPostgresMediaCutover(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user, key, group, account int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash) VALUES('media-metrics@example.invalid','fixture') RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('media-metrics','openai') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'media-metrics-key','fixture',$2) RETURNING id`, user, group).Scan(&key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials) VALUES('media-metrics','openai','apikey','{}') RETURNING id`).Scan(&account))
	now := time.Now().UTC()
	_, err := db.Exec(`UPDATE sub2api_relay_metric_epoch SET started_at=$1,websocket_started_at=$1,media_http_started_at=$2`, now.Add(-2*time.Hour), now.Add(-time.Hour))
	require.NoError(t, err)
	for _, endpoint := range []string{"/v1/embeddings", "/v1/images/generations", "/v1/images/edits", "/v1/tts", "/v1/stt"} {
		for _, created := range []time.Time{now.Add(-90 * time.Minute), now} {
			_, err := db.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,group_id,request_id,model,output_tokens,duration_ms,first_token_ms,actual_cost,inbound_endpoint,request_type,created_at)
VALUES($1,$2,$3,$4,$5,'public-media',100,3000,1000,0,$6,1,$7)`, user, key, account, group, endpoint+created.Format(time.RFC3339Nano), endpoint, created)
			require.NoError(t, err)
		}
		_, err := db.Exec(`INSERT INTO ops_error_logs(user_id,api_key_id,account_id,group_id,request_id,platform,model,error_phase,error_type,status_code,stream,request_type,inbound_endpoint,created_at)
VALUES($1,$2,$3,$4,$5,'openai','public-media','upstream','server_error',502,false,1,$6,$7)`, user, key, account, group, endpoint+"-failed", endpoint, now)
		require.NoError(t, err)
	}
	// Legacy Live-typed rows are governed by their own cutover, even if an
	// old caller supplied an image endpoint. Never reset another epoch.
	_, err = db.Exec(`UPDATE sub2api_relay_metric_epoch SET media_ws_started_at=$1`, now.Add(time.Hour))
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,group_id,request_id,model,duration_ms,actual_cost,inbound_endpoint,request_type,created_at)
VALUES($1,$2,$3,$4,'legacy-live','public-media',3000,0,'/v1/images/generations',5,$5)`, user, key, account, group, now)
	require.NoError(t, err)
	repo := NewModelPlazaMetricsRepository(db)
	samples := []service.RelayPerformanceSample{
		{ID: "00000000-0000-4000-8000-000000000081", GroupID: group, Platform: "openai", Model: "public-media", Success: true, DurationMs: 5000, CreatedAt: now},
		{ID: "00000000-0000-4000-8000-000000000082", GroupID: group, Platform: "openai", Model: "public-media", Success: false, DurationMs: 1000, CreatedAt: now},
	}
	require.NoError(t, repo.(service.RelayPerformanceRepository).WriteRelayPerformance(ctx, samples))
	points, err := repo.ReadModelMetrics(ctx, "public-media", "openai", []int64{group}, now.Add(-3*time.Hour), now.Add(time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 8, points[0].Requests, "5 historical protocols + legacy Live row + 2 final samples, never post-cutover partial bills/ops")
	require.EqualValues(t, 7, points[0].Successes)
	var epoch, repeated time.Time
	require.NoError(t, db.QueryRow(`SELECT media_http_started_at FROM sub2api_relay_metric_epoch`).Scan(&epoch))
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, db.QueryRow(`SELECT media_http_started_at FROM sub2api_relay_metric_epoch`).Scan(&repeated))
	require.True(t, epoch.Equal(repeated), "repeated migrations retain the independent cutover")
	_, err = db.Exec(`DELETE FROM usage_logs`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM ops_error_logs`)
	require.NoError(t, err)
	points, err = repo.ReadModelMetrics(ctx, "public-media", "openai", []int64{group}, now.Add(-3*time.Hour), now.Add(time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 2, points[0].Requests)
	require.EqualValues(t, 1, points[0].Successes)
	require.Nil(t, points[0].TokensPerSecond)
}

func TestRelayPerformanceLocalPostgresMediaWSCutover(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user, key, group, account int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash) VALUES('voice-metrics@example.invalid','fixture') RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('voice-metrics','openai') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'voice-metrics-key','fixture',$2) RETURNING id`, user, group).Scan(&key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials) VALUES('voice-metrics','openai','apikey','{}') RETURNING id`).Scan(&account))
	now := time.Now().UTC()
	_, err := db.Exec(`UPDATE sub2api_relay_metric_epoch SET started_at=$1,websocket_started_at=$1,media_ws_started_at=$2`, now.Add(-2*time.Hour), now.Add(-time.Hour))
	require.NoError(t, err)
	for i, endpoint := range []string{"/v1/realtime", "/realtime", "/v1/live", "/backend-api/codex/realtime/calls"} {
		requestType := 3
		if i >= 2 {
			requestType = 5
		}
		for _, created := range []time.Time{now.Add(-90 * time.Minute), now} {
			_, err := db.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,group_id,request_id,model,output_tokens,duration_ms,actual_cost,inbound_endpoint,request_type,created_at)
VALUES($1,$2,$3,$4,$5,'public-voice',100,10000,0,$6,$7,$8)`, user, key, account, group, endpoint+created.Format(time.RFC3339Nano), endpoint, requestType, created)
			require.NoError(t, err)
		}
	}
	repo := NewModelPlazaMetricsRepository(db)
	sample := service.RelayPerformanceSample{ID: "00000000-0000-4000-8000-000000000091", GroupID: group, Platform: "openai", Model: "public-voice", Success: true, DurationMs: 1000, CreatedAt: now}
	require.NoError(t, repo.(service.RelayPerformanceRepository).WriteRelayPerformance(ctx, []service.RelayPerformanceSample{sample}))
	require.NoError(t, repo.(service.RelayPerformanceRepository).WriteRelayPerformance(ctx, []service.RelayPerformanceSample{sample}))
	points, err := repo.ReadModelMetrics(ctx, "public-voice", "openai", []int64{group}, now.Add(-3*time.Hour), now.Add(time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 5, points[0].Requests, "4 historical sessions + 1 deduplicated terminal turn, never a new session/partial bill")
	require.EqualValues(t, 5, points[0].Successes)
	var epoch, repeated time.Time
	require.NoError(t, db.QueryRow(`SELECT media_ws_started_at FROM sub2api_relay_metric_epoch`).Scan(&epoch))
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, db.QueryRow(`SELECT media_ws_started_at FROM sub2api_relay_metric_epoch`).Scan(&repeated))
	require.True(t, epoch.Equal(repeated))
}
