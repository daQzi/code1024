package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func persistNativeVideoUsageLog(ctx context.Context, tx *sql.Tx, log *service.UsageLog) error {
	inserted, err := (&usageLogRepository{}).createSingle(ctx, tx, log)
	if err != nil || inserted {
		return err
	}
	// A prior process/version may have committed money but lost its best-effort
	// log, or left a zero-cost failed-attempt row. The matching ledger fingerprint
	// was checked before this repair. Preserve the original timestamps/latency.
	r, err := tx.ExecContext(ctx, `UPDATE usage_logs SET total_cost=$1,actual_cost=$2,output_cost=$3,
 output_tokens=$4,video_count=$5,video_duration_seconds=$6,video_resolution=$7,
 account_stats_cost=$8,rate_multiplier=$9,account_rate_multiplier=$10,billing_mode=$11,
 subscription_id=$12,billing_type=$13 WHERE request_id=$14 AND api_key_id=$15
 AND user_id=$16 AND account_id=$17 AND group_id=$18`, log.TotalCost, log.ActualCost, log.OutputCost,
		log.OutputTokens, log.VideoCount, log.VideoDurationSeconds, log.VideoResolution, log.AccountStatsCost,
		log.RateMultiplier, log.AccountRateMultiplier, log.BillingMode, log.SubscriptionID, log.BillingType,
		log.RequestID, log.APIKeyID, log.UserID, log.AccountID, log.GroupID)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("native video usage log ownership mismatch")
	}
	return err
}

func persistNativeVideoFinalUsage(ctx context.Context, tx *sql.Tx, id string, usage *service.NativeVideoFinalUsage) error {
	if usage == nil {
		return nil
	}
	var raw []byte
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT pending_billing,status FROM sub2api_native_video_tasks WHERE id=$1`, id).Scan(&raw, &status); err != nil {
		return err
	}
	var pending *service.GrokVideoPendingBilling
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &pending); err != nil {
		return err
	}
	// Older native jobs keep their original completion path.
	if pending == nil || pending.Financial == nil {
		return nil
	}
	if status == service.GenerationFailed {
		return errors.New("failed native video task has no billable final evidence")
	}
	normalized, err := usage.Normalize(pending)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE sub2api_native_video_settlements SET
 final_usage=COALESCE(final_usage,$2::jsonb),
 status=CASE WHEN status='awaiting_usage' THEN 'ready' ELSE status END,
 next_run_at=CASE WHEN status='awaiting_usage' THEN NOW() ELSE next_run_at END,
 updated_at=NOW() WHERE task_id=$1 AND status<>'ignored'`, id, string(encoded))
	return err
}

func (r *usageBillingRepository) ResolveNativeVideoFinalUsage(ctx context.Context, p *service.NativeVideoBillingSnapshot, usage *service.NativeVideoFinalUsage) (*service.NativeVideoFinalUsage, error) {
	if p == nil || usage == nil || p.Validate() != nil || p.TaskID == "" {
		return nil, errors.New("invalid native video settlement input")
	}
	o := service.NativeVideoObservation{UserID: p.UserID, APIKeyID: p.APIKeyID, GroupID: p.GroupID, AccountID: p.AccountID, Protocol: p.Protocol, UpstreamID: p.TaskID}
	id := service.NativeVideoTaskID(o)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var stored []byte
	err = tx.QueryRowContext(ctx, `SELECT pending_billing FROM sub2api_native_video_tasks WHERE id=$1 FOR UPDATE`, id).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		// A failed history write must not make an accepted legacy task free.
		return usage.Normalize(&service.GrokVideoPendingBilling{Financial: p, Model: p.Model, CreatedAt: p.PricingAt.Format(time.RFC3339Nano)})
	}
	if err != nil {
		return nil, err
	}
	var pending *service.GrokVideoPendingBilling
	if len(stored) != 0 {
		if err := json.Unmarshal(stored, &pending); err != nil {
			return nil, err
		}
	}
	if pending == nil || pending.Financial == nil || pending.ValidateNativeVideoSnapshot() != nil {
		return nil, errors.New("native video durable financial evidence unavailable")
	}
	want, err := json.Marshal(pending.Financial)
	if err != nil {
		return nil, err
	}
	got, err := json.Marshal(p)
	if err != nil || !bytes.Equal(want, got) {
		return nil, service.ErrUsageBillingRequestConflict
	}
	if err := persistNativeVideoFinalUsage(ctx, tx, id, usage); err != nil {
		return nil, err
	}
	var raw []byte
	var status string
	err = tx.QueryRowContext(ctx, `SELECT final_usage,status FROM sub2api_native_video_settlements WHERE task_id=$1`, id).Scan(&raw, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return usage.Normalize(&service.GrokVideoPendingBilling{Financial: p, Model: p.Model, CreatedAt: p.PricingAt.Format(time.RFC3339Nano)})
	}
	if err != nil {
		return nil, err
	}
	if status == "ignored" || len(raw) == 0 {
		return nil, errors.New("native video task has no billable final evidence")
	}
	var final service.NativeVideoFinalUsage
	if err := json.Unmarshal(raw, &final); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &final, nil
}

func (r *generationTaskRepository) ClaimNativeVideoSettlement(ctx context.Context, token string, ttl time.Duration) (*service.NativeVideoSettlement, error) {
	if token == "" || len(token) > 64 || ttl < time.Minute || ttl > 5*time.Minute {
		return nil, errors.New("invalid native video settlement lease")
	}
	t := &service.NativeVideoSettlement{}
	o := &t.Observation
	var pending, final []byte
	err := r.db.QueryRowContext(ctx, `WITH candidate AS (
 SELECT task_id FROM sub2api_native_video_settlements WHERE status='ready'
 AND next_run_at<=NOW() AND (lease_until IS NULL OR lease_until<NOW())
 ORDER BY next_run_at,task_id FOR UPDATE SKIP LOCKED LIMIT 1), leased AS (
 UPDATE sub2api_native_video_settlements s SET lease_token=$1,lease_until=NOW()+$2*INTERVAL '1 second'
 FROM candidate c WHERE s.task_id=c.task_id RETURNING s.*)
 SELECT t.user_id,t.api_key_id,t.group_id,t.account_id,t.upstream_id,t.model,t.platform,t.protocol,
 t.action,t.status,t.delivery_status,t.created_at,t.updated_at,t.pending_billing,
 s.final_usage,s.lease_token,s.failures FROM leased s JOIN sub2api_native_video_tasks t ON t.id=s.task_id`, token, int(ttl.Seconds())).Scan(
		&o.UserID, &o.APIKeyID, &o.GroupID, &o.AccountID, &o.UpstreamID, &o.Model, &o.Platform, &o.Protocol,
		&o.Action, &o.Status, &o.DeliveryStatus, &o.CreatedAt, &o.ObservedAt, &pending, &final, &t.LeaseToken, &t.Failures)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGenerationTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	// Malformed private evidence must enter review, not spin forever after
	// every lease expiry. The service validates it before touching the ledger.
	if err := json.Unmarshal(pending, &t.Pending); err != nil {
		t.Pending = nil
	}
	if err := json.Unmarshal(final, &t.Usage); err != nil {
		t.Usage = nil
	}
	return t, nil
}

func (r *generationTaskRepository) SaveNativeVideoSettlement(ctx context.Context, t *service.NativeVideoSettlement, status, code string, delay time.Duration) error {
	if t == nil || t.LeaseToken == "" || delay < 0 || delay > 5*time.Minute {
		return errors.New("invalid native video settlement update")
	}
	if status != "settled" && status != "logged" && status != "ready" && status != "review" {
		return errors.New("invalid native video settlement status")
	}
	switch code {
	case "", "native_billing_evidence_invalid", "native_billing_retrying", "native_billing_review":
	default:
		return errors.New("invalid native video settlement error code")
	}
	raw, err := r.db.ExecContext(ctx, `UPDATE sub2api_native_video_settlements SET status=$3,last_error_code=$4::text,
 failures=CASE WHEN $4::text='' THEN failures ELSE failures+1 END,next_run_at=NOW()+$5*INTERVAL '1 second',
 lease_token='',lease_until=NULL,updated_at=NOW() WHERE task_id=$1 AND lease_token=$2 AND lease_until>NOW() AND status='ready'`,
		service.NativeVideoTaskID(t.Observation), t.LeaseToken, status, code, int(delay.Seconds()))
	if err != nil {
		return err
	}
	n, err := raw.RowsAffected()
	if err == nil && n != 1 {
		return service.ErrGenerationLeaseLost
	}
	return err
}
