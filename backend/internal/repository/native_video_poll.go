package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *generationTaskRepository) ClaimNativeVideoPoll(ctx context.Context, token string, ttl time.Duration) (*service.NativeVideoPollTask, error) {
	if token == "" || len(token) > 64 || ttl < time.Minute || ttl > 5*time.Minute {
		return nil, errors.New("invalid native video poll lease")
	}
	t := &service.NativeVideoPollTask{}
	o := &t.Observation
	var pending []byte
	err := r.db.QueryRowContext(ctx, `WITH candidate AS (
 SELECT task_id FROM sub2api_native_video_polls WHERE monitor_status='active'
 AND next_run_at<=NOW() AND (lease_until IS NULL OR lease_until<NOW())
 ORDER BY next_run_at,task_id FOR UPDATE SKIP LOCKED LIMIT 1
 ), leased AS (
 UPDATE sub2api_native_video_polls p SET lease_token=$1,lease_until=NOW()+$2*INTERVAL '1 second',attempts=attempts+1
 FROM candidate c WHERE p.task_id=c.task_id RETURNING p.*)
 SELECT t.user_id,t.api_key_id,t.group_id,t.account_id,t.upstream_id,t.model,t.platform,t.protocol,t.action,t.status,t.delivery_status,t.created_at,t.updated_at,
 p.connection_hash,p.lease_token,p.failures,p.expires_at,t.pending_billing,
 EXISTS(SELECT 1 FROM sub2api_native_video_settlements s WHERE s.task_id=t.id AND s.status='awaiting_usage')
 FROM leased p JOIN sub2api_native_video_tasks t ON t.id=p.task_id`, token, int(ttl.Seconds())).Scan(
		&o.UserID, &o.APIKeyID, &o.GroupID, &o.AccountID, &o.UpstreamID, &o.Model, &o.Platform, &o.Protocol, &o.Action, &o.Status, &o.DeliveryStatus, &o.CreatedAt, &o.ObservedAt,
		&t.ConnectionHash, &t.LeaseToken, &t.Failures, &t.ExpiresAt, &pending, &t.AwaitingUsage)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGenerationTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(pending) != 0 {
		if err := json.Unmarshal(pending, &t.PendingBilling); err != nil {
			t.PendingBilling = nil // the worker will pause invalid funding evidence
		}
	}
	return t, nil
}

func (r *generationTaskRepository) SaveNativeVideoPoll(ctx context.Context, t *service.NativeVideoPollTask, u service.NativeVideoPollUpdate) error {
	if t == nil || t.LeaseToken == "" || t.Observation.Create || t.Observation.Validate() != nil || u.ObservedAt.IsZero() || u.Delay < 0 || u.Delay > 5*time.Minute {
		return errors.New("invalid native video poll update")
	}
	if u.MonitorStatus != service.NativeVideoMonitorActive && u.MonitorStatus != service.NativeVideoMonitorComplete && u.MonitorStatus != service.NativeVideoMonitorReview {
		return errors.New("invalid native video monitor status")
	}
	// Never store arbitrary transport messages or upstream response text.
	switch u.ErrorCode {
	case "", "native_monitor_timeout", "native_monitor_account_unavailable", "native_monitor_connection_changed", "native_monitor_auth_failed", "native_monitor_capacity", "native_monitor_upstream_error", "native_monitor_response_limit", "native_monitor_unrecognized", "native_monitor_usage_pending":
	default:
		return errors.New("invalid native video monitor error code")
	}
	o := t.Observation
	if u.State != nil {
		o.Status, o.DeliveryStatus, o.ObservedAt = u.State.Status, u.State.DeliveryStatus, u.ObservedAt
		if err := o.Validate(); err != nil {
			return err
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id := service.NativeVideoTaskID(o)
	// The foreground observer locks history before the monitor. Use the same
	// order here so a client poll and a worker cannot deadlock each other.
	var lockedID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sub2api_native_video_tasks WHERE id=$1 FOR UPDATE`, id).Scan(&lockedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrGenerationLeaseLost
		}
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sub2api_native_video_polls SET monitor_status=$3,last_error_code=$4::text,
 failures=CASE WHEN $4::text='' THEN 0 WHEN $4::text='native_monitor_capacity' THEN failures ELSE failures+1 END, next_run_at=NOW()+$5*INTERVAL '1 second',
 lease_token='',lease_until=NULL,updated_at=$6 WHERE task_id=$1 AND lease_token=$2 AND lease_until>NOW() AND monitor_status='active'`, id, t.LeaseToken, u.MonitorStatus, u.ErrorCode, int(u.Delay.Seconds()), u.ObservedAt)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrGenerationLeaseLost
	}
	if u.State != nil {
		if o.Status == service.GenerationSucceeded {
			if err := persistNativeVideoFinalUsage(ctx, tx, id, u.State.FinalUsage); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, nativeVideoObservationUpdateSQL, id, o.Status, o.DeliveryStatus, o.ObservedAt); err != nil {
			return err
		}
		if o.Status == service.GenerationFailed {
			if _, err := tx.ExecContext(ctx, `UPDATE sub2api_native_video_settlements s SET status='ignored',updated_at=NOW()
 FROM sub2api_native_video_tasks t WHERE s.task_id=t.id AND t.id=$1 AND t.status='failed'
 AND s.status='awaiting_usage' AND s.final_usage IS NULL`, id); err != nil {
				return err
			}
		}
	}
	// Completion of delivery is not proof of final billable usage. Keep a
	// prematurely completed monitor recoverable even if a caller gets this
	// distinction wrong; financial work must not disappear with its poll job.
	if u.MonitorStatus == service.NativeVideoMonitorComplete {
		if _, err := tx.ExecContext(ctx, `UPDATE sub2api_native_video_polls p
 SET monitor_status='active',last_error_code='native_monitor_usage_pending'
 WHERE p.task_id=$1 AND EXISTS(SELECT 1 FROM sub2api_native_video_settlements s
 WHERE s.task_id=p.task_id AND s.status='awaiting_usage')`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
