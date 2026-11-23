package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *generationTaskRepository) DeleteGenerationResult(ctx context.Context, id string, user, key, group int64, videoOnly bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const identity = `id=$1 AND user_id=$2 AND api_key_id=$3 AND group_id=$4 AND plugin_version_id>0`
	var protocol, status, billing string
	var deleted sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT protocol,status,billing_status,public_deleted_at FROM sub2api_generation_tasks WHERE `+identity+` FOR UPDATE`, id, user, key, group).Scan(&protocol, &status, &billing, &deleted)
	if errors.Is(err, sql.ErrNoRows) || err == nil && videoOnly && !service.IsVideoGenerationProtocol(protocol) {
		return service.ErrGenerationTaskNotFound
	}
	if err != nil || deleted.Valid {
		return err
	}
	if (status != service.GenerationSucceeded && status != service.GenerationFailed) || billing != service.GenerationBillingSettled {
		return service.ErrGenerationTaskResultNotDeletable
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sub2api_generation_tasks SET public_deleted_at=NOW() WHERE `+identity, id, user, key, group); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sub2api_generation_events(task_id,event_key,event_type,payload) VALUES($1,'public_result_deleted','result_deleted','{}'::jsonb)`, id); err != nil {
		return err
	}
	return tx.Commit()
}
