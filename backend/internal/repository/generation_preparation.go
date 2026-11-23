package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *generationTaskRepository) CheckpointPreparation(ctx context.Context, task *service.GenerationTask) error {
	if task.PreparationEncrypted == "" || len(task.PreparationEncrypted) > 98304 {
		return errors.New("invalid encrypted preparation checkpoint")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE sub2api_generation_tasks SET preparation_encrypted=$3
 WHERE id=$1 AND lease_token=$2 AND lease_until>NOW() AND
 ((preparation_status='pending' AND status='submitting' AND billing_status='held') OR
  (preparation_status='cleanup' AND billing_status='settled'))`, task.ID, task.LeaseToken, task.PreparationEncrypted)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.ErrGenerationLeaseLost
	}
	return err
}

func (r *generationTaskRepository) ClaimPreparationCleanup(ctx context.Context, token string, ttl time.Duration) (*service.GenerationTask, error) {
	if token == "" || len(token) > 64 || ttl < time.Minute || ttl > 5*time.Minute {
		return nil, errors.New("invalid preparation cleanup lease")
	}
	return scanGenerationTask(r.db.QueryRowContext(ctx, `WITH candidate AS (
 SELECT id FROM sub2api_generation_tasks WHERE preparation_status='cleanup' AND billing_status='settled'
 AND next_run_at<=NOW() AND (lease_until IS NULL OR lease_until<NOW())
 ORDER BY next_run_at,id FOR UPDATE SKIP LOCKED LIMIT 1
 ) UPDATE sub2api_generation_tasks t SET lease_token=$1,lease_until=NOW()+$2*INTERVAL '1 second'
 FROM candidate c WHERE t.id=c.id RETURNING `+prefixGenerationColumns("t"), token, int(ttl.Seconds())))
}

func (r *generationTaskRepository) FinishPreparationCleanup(ctx context.Context, task *service.GenerationTask, done bool, delay time.Duration) error {
	if delay < time.Second || delay > time.Hour {
		return errors.New("invalid preparation cleanup delay")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE sub2api_generation_tasks
 SET preparation_status=CASE WHEN $3 THEN 'cleaned' ELSE 'cleanup' END,
 preparation_encrypted=CASE WHEN $3 THEN '' ELSE preparation_encrypted END,
 next_run_at=NOW()+$4*INTERVAL '1 second',lease_until=NULL,lease_token=''
 WHERE id=$1 AND lease_token=$2 AND lease_until>NOW() AND preparation_status='cleanup' AND billing_status='settled'`, task.ID, task.LeaseToken, done, int(delay.Seconds()))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.ErrGenerationLeaseLost
	}
	return err
}
