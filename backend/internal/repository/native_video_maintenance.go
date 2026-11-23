package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *generationTaskRepository) GetNativeVideoMaintenance(ctx context.Context, id string) (*service.NativeVideoMaintenance, error) {
	task := &service.NativeVideoMaintenance{}
	o := &task.Observation
	var pending, usage []byte
	err := r.db.QueryRowContext(ctx, `SELECT t.user_id,t.api_key_id,t.group_id,t.account_id,t.upstream_id,t.protocol,t.status,t.delivery_status,t.created_at,t.updated_at,
 t.pending_billing,COALESCE(p.connection_hash,''),COALESCE(p.monitor_status,''),COALESCE(s.status,''),s.final_usage
 FROM sub2api_native_video_tasks t LEFT JOIN sub2api_native_video_polls p ON p.task_id=t.id
 LEFT JOIN sub2api_native_video_settlements s ON s.task_id=t.id WHERE t.id=$1`, id).Scan(
		&o.UserID, &o.APIKeyID, &o.GroupID, &o.AccountID, &o.UpstreamID, &o.Protocol, &o.Status, &o.DeliveryStatus, &o.CreatedAt, &o.ObservedAt,
		&pending, &task.ConnectionHash, &task.MonitorStatus, &task.SettlementStatus, &usage)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGenerationTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(pending) != 0 && json.Unmarshal(pending, &task.Pending) != nil || len(usage) != 0 && json.Unmarshal(usage, &task.Usage) != nil {
		return nil, service.ErrNativeVideoMaintenanceConflict
	}
	return task, nil
}

func (r *generationTaskRepository) ResumeNativeVideo(ctx context.Context, id string, actor int64, action, reason, hash string) error {
	if actor <= 0 || len(strings.TrimSpace(reason)) < 4 || len(reason) > 2000 || strings.ContainsRune(reason, 0) || action != "resume_poll" && action != "retry_settlement" {
		return service.ErrNativeVideoMaintenanceConflict
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, delivery string
	if err := tx.QueryRowContext(ctx, `SELECT status,delivery_status FROM sub2api_native_video_tasks WHERE id=$1 FOR UPDATE`, id).Scan(&status, &delivery); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrGenerationTaskNotFound
		}
		return err
	}
	var monitor, connection string
	var pollLease sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT monitor_status,connection_hash,lease_until FROM sub2api_native_video_polls WHERE task_id=$1 FOR UPDATE`, id).Scan(&monitor, &connection, &pollLease)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var settlement string
	var settlementLease sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT status,lease_until FROM sub2api_native_video_settlements WHERE task_id=$1 FOR UPDATE`, id).Scan(&settlement, &settlementLease)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	if status == service.GenerationFailed || pollLease.Valid && pollLease.Time.After(now) || settlementLease.Valid && settlementLease.Time.After(now) {
		return service.ErrNativeVideoMaintenanceConflict
	}
	if action == "resume_poll" {
		if monitor != service.NativeVideoMonitorReview || hash == "" || connection != hash || status == service.GenerationSucceeded && delivery != service.GenerationDeliveryPending && settlement != "awaiting_usage" {
			return service.ErrNativeVideoMaintenanceConflict
		}
		_, err = tx.ExecContext(ctx, `UPDATE sub2api_native_video_polls SET monitor_status='active',next_run_at=NOW(),expires_at=NOW()+INTERVAL '24 hours',
 failures=0,last_error_code='',lease_token='',lease_until=NULL,updated_at=NOW() WHERE task_id=$1`, id)
	} else {
		if settlement != "review" {
			return service.ErrNativeVideoMaintenanceConflict
		}
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE sub2api_native_video_settlements SET status='ready',next_run_at=NOW(),failures=0,
 last_error_code='',lease_token='',lease_until=NULL,updated_at=NOW() WHERE task_id=$1 AND final_usage IS NOT NULL`, id)
		if err == nil {
			count, countErr := result.RowsAffected()
			if countErr != nil {
				return countErr
			}
			if count != 1 {
				return service.ErrNativeVideoMaintenanceConflict
			}
		}
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sub2api_native_video_actions(task_id,actor_id,action,reason) VALUES($1,$2,$3,$4)`, id, actor, action, reason)
	if err != nil {
		return err
	}
	return tx.Commit()
}
