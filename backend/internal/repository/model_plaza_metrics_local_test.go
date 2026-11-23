package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGenerationLocalPostgresPerformanceOutcomes(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user, key, group, account int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash) VALUES('metrics@example.invalid','fixture') RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('metrics','openai') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'metrics-key','fixture',$2) RETURNING id`, user, group).Scan(&key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials) VALUES('metrics','openai','apikey','{}') RETURNING id`).Scan(&account))
	usage := func(id string, output, duration int, first any) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,group_id,request_id,model,output_tokens,duration_ms,first_token_ms,actual_cost)
 VALUES($1,$2,$3,$4,$5,'public-model',$6,$7,$8,0)`, user, key, account, group, id, output, duration, first)
		require.NoError(t, err)
	}
	failure := func(id string, status int, business bool) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO ops_error_logs(user_id,api_key_id,account_id,group_id,request_id,platform,model,error_phase,error_type,status_code,is_business_limited,stream)
 VALUES($1,$2,$3,$4,$5,'openai','public-model','upstream','server_error',$6,$7,true)`, user, key, account, group, id, status, business)
		require.NoError(t, err)
	}
	usage("free", 100, 3000, 1000)
	usage("weighted", 200, 5000, 1000)
	usage("no-ttft", 900, 9000, nil)
	usage("stream-failed", 50, 2000, 1000)
	failure("stream-failed", 200, false) // in-band SSE error overrides charged partial usage
	failure("stream-failed", 200, false) // final outcome is counted once
	usage("bad-input", 1, 100, 10)
	failure("bad-input", 400, false)
	usage("cancelled", 1, 100, 10)
	failure("cancelled", 499, false)
	failure("upstream-limit", 429, false)
	failure("local-limit", 429, true)
	repo := NewModelPlazaMetricsRepository(db)
	start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Minute)
	points, err := repo.ReadModelMetrics(ctx, "public-model", "openai", []int64{group}, start, end)
	require.NoError(t, err)
	var total service.PlazaMetricsPoint
	for _, point := range points {
		if point.GroupID == 0 && point.Bucket == nil {
			total = point
		}
	}
	require.EqualValues(t, 5, total.Requests)
	require.EqualValues(t, 3, total.Successes, "free successes count; partial SSE usage must not mask failures")
	require.InDelta(t, 17000.0/3, *total.AverageMs, 0.001)
	require.InDelta(t, 50, *total.TokensPerSecond, 0.001, "sum tokens / sum generation duration, without TTFT or unknown durations")
	summaries, err := repo.ReadModelSummaries(ctx, []service.PlazaMetricsScope{{ID: "model", Model: "public-model", Platform: "openai", GroupIDs: []int64{group}}}, start, end)
	require.NoError(t, err)
	require.Equal(t, total.Requests, summaries["model"].Requests)
	require.NotEmpty(t, summaries["model"].Recent)
	var count int64
	for _, bucket := range summaries["model"].Recent {
		count += bucket.Requests
	}
	require.Equal(t, total.Requests, count)
}

func TestRelayPerformanceLocalPostgresFinalSamplesAndNativeVideo(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user, key, group, account int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash) VALUES('final-metrics@example.invalid','fixture') RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('final-metrics','openai') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'final-metrics-key','fixture',$2) RETURNING id`, user, group).Scan(&key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials) VALUES('final-metrics','openai','apikey','{}') RETURNING id`).Scan(&account))
	now := time.Now().UTC()
	// A post-cutover partial bill and an SSE failure cannot manufacture success,
	// even if the independent telemetry write was unavailable.
	_, err := db.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,group_id,request_id,model,output_tokens,duration_ms,first_token_ms,actual_cost,inbound_endpoint)
 VALUES($1,$2,$3,$4,'partial','public-model',100,1000,100,0,'/v1/responses')`, user, key, account, group)
	require.NoError(t, err)
	writer := NewModelPlazaMetricsRepository(db).(service.RelayPerformanceRepository)
	first := int64(1000)
	samples := []service.RelayPerformanceSample{
		{ID: "00000000-0000-4000-8000-000000000001", GroupID: group, Platform: "openai", Model: "public-model", Success: true, DurationMs: 3000, FirstTokenMs: &first, OutputTokens: 100, CreatedAt: now},
		{ID: "00000000-0000-4000-8000-000000000002", GroupID: group, Platform: "openai", Model: "public-model", Success: false, DurationMs: 2000, CreatedAt: now},
		{ID: "00000000-0000-4000-8000-000000000003", GroupID: group, Platform: "grok", Model: "public-model", Success: true, DurationMs: 5, CreatedAt: now},
		{ID: "00000000-0000-4000-8000-000000000004", GroupID: group, Platform: "openai", Model: "other-model", Success: true, DurationMs: 5, CreatedAt: now},
	}
	require.NoError(t, writer.WriteRelayPerformance(ctx, samples))
	require.NoError(t, writer.WriteRelayPerformance(ctx, samples))
	o := service.NativeVideoObservation{UserID: user, APIKeyID: key, GroupID: group, AccountID: account, UpstreamID: "native-metrics", Model: "public-model", Platform: "openai", Protocol: service.GenerationProtocolArk, Action: "create", Create: true, Status: service.GenerationQueued, DeliveryStatus: service.GenerationDeliveryPending, CreatedAt: now.Add(-10 * time.Second), ObservedAt: now.Add(-10 * time.Second)}
	history := NewGenerationTaskRepository(db).(service.NativeVideoTaskRepository)
	require.NoError(t, history.ObserveNativeVideo(ctx, o))
	o.Create = false
	o.Status = service.GenerationSucceeded
	o.DeliveryStatus = service.GenerationDeliveryReady
	o.ObservedAt = now
	require.NoError(t, history.ObserveNativeVideo(ctx, o))
	require.NoError(t, history.ObserveNativeVideo(ctx, o))
	repo := NewModelPlazaMetricsRepository(db)
	summaries, err := repo.ReadModelSummaries(ctx, []service.PlazaMetricsScope{{ID: "public", Model: "public-model", Platform: "openai", GroupIDs: []int64{group}}}, now.Add(-time.Hour), now.Add(time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 3, summaries["public"].Requests)
	require.EqualValues(t, 2, summaries["public"].Successes)
	require.InDelta(t, 6500, *summaries["public"].AverageMs, 0.01)
	require.InDelta(t, 50, *summaries["public"].TokensPerSecond, 0.01)
	// Clearing billing/ops history cannot erase post-cutover final telemetry.
	_, err = db.Exec(`DELETE FROM usage_logs`)
	require.NoError(t, err)
	points, err := repo.ReadModelMetrics(ctx, "public-model", "openai", []int64{group}, now.Add(-time.Hour), now.Add(time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 3, points[0].Requests)
	var epoch time.Time
	require.NoError(t, db.QueryRow(`SELECT started_at FROM sub2api_relay_metric_epoch`).Scan(&epoch))
	require.NoError(t, ApplyMigrations(ctx, db))
	var repeated time.Time
	require.NoError(t, db.QueryRow(`SELECT started_at FROM sub2api_relay_metric_epoch`).Scan(&repeated))
	require.True(t, epoch.Equal(repeated))
	old := samples[0]
	old.ID = "00000000-0000-4000-8000-000000000005"
	old.CreatedAt = now.Add(-49 * time.Hour)
	require.NoError(t, writer.WriteRelayPerformance(ctx, []service.RelayPerformanceSample{old}))
	require.NoError(t, writer.CleanupRelayPerformance(ctx, now.Add(-48*time.Hour), 1))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sub2api_relay_performance_samples`).Scan(&count))
	require.Equal(t, 4, count)
}

func TestModelPlazaUpdatesLocalPostgresScopeAndVersion(t *testing.T) {
	db := generationLocalDB(t)
	repo := NewModelPlazaMetricsRepository(db).(service.ModelPerformanceVersionRepository)
	scope := []service.PlazaMetricsScope{{ID: "public", Model: "model", Platform: "openai", GroupIDs: []int64{7}}}
	before, err := repo.ReadModelPerformanceVersion(context.Background(), scope)
	require.NoError(t, err)
	writer := NewModelPlazaMetricsRepository(db).(service.RelayPerformanceRepository)
	sample := service.RelayPerformanceSample{ID: "00000000-0000-4000-8000-000000000010", GroupID: 8, Platform: "openai", Model: "model", Success: true, DurationMs: 1, CreatedAt: time.Now()}
	require.NoError(t, writer.WriteRelayPerformance(context.Background(), []service.RelayPerformanceSample{sample}))
	current, err := repo.ReadModelPerformanceVersion(context.Background(), scope)
	require.NoError(t, err)
	require.Equal(t, before, current, "another group must not notify this scope")
	sample.ID = "00000000-0000-4000-8000-000000000011"
	sample.GroupID = 7
	require.NoError(t, writer.WriteRelayPerformance(context.Background(), []service.RelayPerformanceSample{sample}))
	current, err = repo.ReadModelPerformanceVersion(context.Background(), scope)
	require.NoError(t, err)
	require.NotEqual(t, before, current)
	repeated, err := repo.ReadModelPerformanceVersion(context.Background(), scope)
	require.NoError(t, err)
	require.Equal(t, current, repeated)
	scope[0].GroupIDs = []int64{7, 8}
	_, err = repo.ReadModelPerformanceVersion(context.Background(), scope)
	require.Error(t, err)
}
