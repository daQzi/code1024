package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *generationTaskRepository) ObserveNativeVideo(ctx context.Context, o service.NativeVideoObservation) error {
	if err := o.Validate(); err != nil {
		return err
	}
	id := service.NativeVideoTaskID(o)
	if o.Create {
		fields, err := json.Marshal(o.Fields)
		if err != nil {
			return err
		}
		var pending any
		if o.PendingBilling != nil {
			encoded, err := json.Marshal(o.PendingBilling)
			if err != nil {
				return err
			}
			pending = string(encoded)
		}
		var final any
		if o.Status == service.GenerationSucceeded && o.FinalUsage != nil && o.PendingBilling != nil && o.PendingBilling.Financial != nil {
			u, err := o.FinalUsage.Normalize(o.PendingBilling)
			if err != nil {
				return err
			}
			encoded, err := json.Marshal(u)
			if err != nil {
				return err
			}
			final = string(encoded)
		}
		_, err = r.db.ExecContext(ctx, `WITH created AS (INSERT INTO sub2api_native_video_tasks
 (id,user_id,api_key_id,group_id,account_id,upstream_id,ledger_request_id,model,platform,protocol,action,public_fields,status,delivery_status,created_at,updated_at,running_at,finished_at,pending_billing)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13::text,$14::text,$15,$16,
 CASE WHEN $13='running' THEN $16::timestamptz END,
 CASE WHEN $13 IN('succeeded','failed') THEN $16::timestamptz END,$17::jsonb)
 ON CONFLICT(id) DO NOTHING RETURNING id)
 , monitored AS (INSERT INTO sub2api_native_video_polls(task_id,connection_hash,next_run_at,expires_at,updated_at)
 SELECT id,$18::text,$16::timestamptz+INTERVAL '15 seconds',$16::timestamptz+INTERVAL '24 hours',$16 FROM created
 WHERE $18::text<>'' ON CONFLICT(task_id) DO NOTHING RETURNING task_id)
 INSERT INTO sub2api_native_video_settlements(task_id,updated_at,final_usage,status)
 SELECT id,$16,$19::jsonb,CASE WHEN $13='failed' THEN 'ignored' WHEN $19::jsonb IS NOT NULL THEN 'ready' ELSE 'awaiting_usage' END
 FROM created WHERE $18::text<>'' AND $17::jsonb->'financial' IS NOT NULL
 ON CONFLICT(task_id) DO NOTHING`, id, o.UserID, o.APIKeyID, o.GroupID, o.AccountID, o.UpstreamID,
			service.StableGrokVideoBillingRequestID(o.UpstreamID), o.Model, o.Platform, o.Protocol, o.Action, fields, o.Status, o.DeliveryStatus, o.CreatedAt, o.ObservedAt, pending, o.PollConnectionHash, final)
		return err
	}
	// Only update a task whose creation this gateway observed. Polling an old
	// task must not invent its owner, model, start time or completion duration.
	// Row locks and monotonic transitions make racing/repeated polls harmless.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var lockedID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM sub2api_native_video_tasks WHERE id=$1 FOR UPDATE`, id).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if o.Status == service.GenerationSucceeded {
		if err := persistNativeVideoFinalUsage(ctx, tx, id, o.FinalUsage); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `WITH observed AS (`+nativeVideoObservationUpdateSQL+` RETURNING id,status,delivery_status)
 UPDATE sub2api_native_video_polls p SET monitor_status='complete',last_error_code='',lease_token='',lease_until=NULL,updated_at=$4
 FROM observed o WHERE p.task_id=o.id AND p.monitor_status<>'complete'
 AND (o.status='failed' OR (o.status='succeeded' AND o.delivery_status<>'pending'
 AND NOT EXISTS(SELECT 1 FROM sub2api_native_video_settlements s WHERE s.task_id=o.id AND s.status='awaiting_usage')))`, id, o.Status, o.DeliveryStatus, o.ObservedAt)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sub2api_native_video_settlements s SET status='ignored',updated_at=NOW()
 FROM sub2api_native_video_tasks t WHERE s.task_id=t.id AND t.id=$1 AND t.status='failed' AND s.status='awaiting_usage' AND s.final_usage IS NULL`, id); err != nil {
		return err
	}
	return tx.Commit()
}

const nativeVideoObservationUpdateSQL = `UPDATE sub2api_native_video_tasks SET
 status=$2::text,delivery_status=$3::text,updated_at=$4,
 running_at=CASE WHEN $2='running' THEN COALESCE(running_at,$4) ELSE running_at END,
 finished_at=CASE WHEN $2 IN('succeeded','failed') THEN COALESCE(finished_at,$4) ELSE finished_at END
 WHERE id=$1 AND (status NOT IN('succeeded','failed') OR
 (status='succeeded' AND $2='succeeded' AND delivery_status<>'ready' AND $3='ready')) AND updated_at<=$4
 AND NOT(status='running' AND $2='queued')`

func (r *generationTaskRepository) GetNativeVideoRecovery(ctx context.Context, q service.NativeVideoLookup) (*service.NativeVideoRecovery, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT account_id,pending_billing FROM sub2api_native_video_tasks
 WHERE user_id=$1 AND api_key_id=$2 AND group_id=$3 AND protocol=$4 AND upstream_id=$5 LIMIT 2`, q.UserID, q.APIKeyID, q.GroupID, q.Protocol, q.UpstreamID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var found *service.NativeVideoRecovery
	var payload []byte
	for rows.Next() {
		if found != nil {
			return nil, service.ErrNativeVideoRecoveryAmbiguous
		}
		found = &service.NativeVideoRecovery{}
		if err := rows.Scan(&found.AccountID, &payload); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, service.ErrGenerationTaskNotFound
	}
	if len(payload) != 0 {
		// Decode only the private typed snapshot. Older rows deliberately stay
		// nil; prompts, public fields and current prices cannot fill the gap.
		if err := json.Unmarshal(payload, &found.PendingBilling); err != nil {
			return nil, errors.New("invalid stored native video billing snapshot")
		}
		if found.PendingBilling == nil {
			return nil, errors.New("invalid stored native video billing snapshot")
		}
		if err := found.PendingBilling.ValidateNativeVideoSnapshot(); err != nil {
			return nil, err
		}
	}
	return found, nil
}
