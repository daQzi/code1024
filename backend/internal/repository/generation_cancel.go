package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *generationTaskRepository) CancelQueuedTask(ctx context.Context, id string, user, key, group int64) (*service.GenerationTask, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	const identity = `id=$1 AND user_id=$2 AND api_key_id=$3 AND group_id=$4 AND plugin_version_id>0 AND public_deleted_at IS NULL`
	current, err := scanGenerationTask(tx.QueryRowContext(ctx, `SELECT `+prefixGenerationColumns("")+` FROM sub2api_generation_tasks WHERE `+identity+` FOR UPDATE`, id, user, key, group))
	if err != nil {
		return nil, err
	}
	if current.Status == service.GenerationFailed && current.ErrorCode == service.GenerationCanceledBeforeSubmit {
		return current, nil
	}
	if current.Status != service.GenerationQueued || current.BillingStatus != service.GenerationBillingHeld || current.UpstreamID != "" || current.LeaseToken != "" {
		return nil, service.ErrGenerationTaskCannotCancel
	}
	// An eligible queued job may have finished reference preparation, but
	// never started paid submission. Keep its checkpoint for the cleanup
	// worker after zero settlement, using the fixed original connection.
	canceled, err := scanGenerationTask(tx.QueryRowContext(ctx, `UPDATE sub2api_generation_tasks SET
	 status='failed',billing_status='pending',finished_at=NOW(),updated_at=NOW(),next_run_at=NOW(),error_code=$5,
	 lease_until=NULL,lease_token='',
	 error_message='The task was canceled before submission.',
	 preparation_status=CASE WHEN preparation_status IN ('pending','ready') THEN 'cleanup' ELSE preparation_status END
	 WHERE `+identity+` RETURNING `+prefixGenerationColumns(""), id, user, key, group, service.GenerationCanceledBeforeSubmit))
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sub2api_generation_events(task_id,event_key,event_type,payload)
	 VALUES($1,'cancel_before_submit','task_canceled','{}'::jsonb)`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return canceled, nil
}

func (r *generationTaskRepository) ConfirmUpstreamCancelTask(ctx context.Context, id string, user, key, group int64) (*service.GenerationTask, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	const identity = `id=$1 AND user_id=$2 AND api_key_id=$3 AND group_id=$4 AND plugin_version_id>0 AND public_deleted_at IS NULL`
	current, err := scanGenerationTask(tx.QueryRowContext(ctx, `SELECT `+prefixGenerationColumns("")+` FROM sub2api_generation_tasks WHERE `+identity+` FOR UPDATE`, id, user, key, group))
	if err != nil {
		return nil, err
	}
	if current.Status == service.GenerationFailed && (current.ErrorCode == service.GenerationCanceledUpstream || current.ErrorCode == service.GenerationCanceledBeforeSubmit) {
		return current, nil
	}
	if (current.Status != service.GenerationRunning && current.Status != service.GenerationSubmitting) || current.BillingStatus != service.GenerationBillingHeld || current.UpstreamID == "" {
		return nil, service.ErrGenerationTaskCannotCancel
	}
	canceled, err := scanGenerationTask(tx.QueryRowContext(ctx, `UPDATE sub2api_generation_tasks SET
	 status='failed',billing_status='pending',finished_at=NOW(),updated_at=NOW(),next_run_at=NOW(),error_code=$5,
	 lease_until=NULL,lease_token='',
	 error_message='The task was canceled upstream.',
	 preparation_status=CASE WHEN preparation_status IN ('pending','ready') THEN 'cleanup' ELSE preparation_status END
	 WHERE `+identity+` RETURNING `+prefixGenerationColumns(""), id, user, key, group, service.GenerationCanceledUpstream))
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sub2api_generation_events(task_id,event_key,event_type,payload)
	 VALUES($1,'cancel_upstream_confirmed','task_canceled','{}'::jsonb)`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return canceled, nil
}
