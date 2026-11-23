package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
)

// The task row, wallet, usage ledger, quota projection and Redis delivery are
// one transaction. Locking the quota row before allocating its event orders
// cursors for each quota identity, even when different instances settle tasks.
func applyGenerationQuotaTx(ctx context.Context, tx *sql.Tx, taskID string, userID int64, amount decimal.Decimal) error {
	if !amount.IsPositive() {
		return nil
	}
	var quotaID int64
	var platform string
	var daily, weekly, monthly sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT q.id,t.platform,q.daily_window_start,q.weekly_window_start,q.monthly_window_start
 FROM user_platform_quotas q JOIN sub2api_generation_tasks t ON t.user_id=q.user_id AND t.platform=q.platform
 WHERE t.id=$1 AND q.user_id=$2 AND q.deleted_at IS NULL
 AND (q.daily_limit_usd IS NOT NULL OR q.weekly_limit_usd IS NOT NULL OR q.monthly_limit_usd IS NOT NULL)
 FOR UPDATE OF q`, taskID, userID).Scan(&quotaID, &platform, &daily, &weekly, &monthly)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // An unconfigured/deleted quota must not be created by usage.
	}
	if err != nil {
		return err
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	day, week, month := timezone.StartOfDay(now), timezone.StartOfWeek(now), now
	if daily.Valid && !daily.Time.Before(day) {
		day = daily.Time
	}
	if weekly.Valid && !weekly.Time.Before(week) {
		week = weekly.Time
	}
	if monthly.Valid && now.Sub(monthly.Time) < 30*24*time.Hour {
		month = monthly.Time
	}
	var cursor int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO sub2api_generation_quota_outbox(task_id,quota_id,user_id,platform,amount,daily_start,weekly_start,monthly_start,created_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, taskID, quotaID, userID, platform, amount.StringFixed(8), day, week, month, now).Scan(&cursor); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE user_platform_quotas SET
 daily_usage_usd=CASE WHEN daily_window_start IS NOT DISTINCT FROM $4::timestamptz THEN daily_usage_usd+$2 ELSE $2 END,
 weekly_usage_usd=CASE WHEN weekly_window_start IS NOT DISTINCT FROM $5::timestamptz THEN weekly_usage_usd+$2 ELSE $2 END,
 monthly_usage_usd=CASE WHEN monthly_window_start IS NOT DISTINCT FROM $6::timestamptz THEN monthly_usage_usd+$2 ELSE $2 END,
 daily_window_start=$4,weekly_window_start=$5,monthly_window_start=$6,generation_cursor=$3,updated_at=$7
 WHERE id=$1`, quotaID, amount.StringFixed(8), cursor, day, week, month, now)
	return err
}

func (r *generationTaskRepository) ClaimGenerationQuota(ctx context.Context, token string, lease time.Duration) (*service.GenerationQuotaEvent, error) {
	if token == "" || len(token) > 64 || lease < time.Second || lease > time.Minute {
		return nil, errors.New("invalid generation quota lease")
	}
	event := &service.GenerationQuotaEvent{LeaseToken: token}
	err := r.db.QueryRowContext(ctx, `WITH due AS (
 SELECT e.id FROM sub2api_generation_quota_outbox e
 WHERE e.delivered_at IS NULL AND e.next_run_at<=clock_timestamp()
 AND (e.lease_until IS NULL OR e.lease_until<clock_timestamp())
 AND NOT EXISTS(SELECT 1 FROM sub2api_generation_quota_outbox earlier
   WHERE earlier.quota_id=e.quota_id AND earlier.id<e.id AND earlier.delivered_at IS NULL)
 ORDER BY e.id FOR UPDATE OF e SKIP LOCKED LIMIT 1)
 UPDATE sub2api_generation_quota_outbox e SET lease_token=$1,lease_until=clock_timestamp()+$2*INTERVAL '1 second',attempts=attempts+1
 FROM due WHERE e.id=due.id
 RETURNING e.id,e.task_id,e.quota_id,e.user_id,e.platform,e.amount::text,e.daily_start,e.weekly_start,e.monthly_start,e.created_at`, token, int(lease.Seconds())).Scan(&event.ID, &event.TaskID, &event.QuotaID, &event.UserID, &event.Platform, &event.Amount, &event.DailyStart, &event.WeeklyStart, &event.MonthlyStart, &event.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGenerationTaskNotFound
	}
	return event, err
}

func (r *generationTaskRepository) CompleteGenerationQuota(ctx context.Context, event *service.GenerationQuotaEvent, delivered bool) error {
	if event == nil || event.ID <= 0 || event.LeaseToken == "" {
		return errors.New("invalid generation quota delivery")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE sub2api_generation_quota_outbox SET
 delivered_at=CASE WHEN $3 THEN clock_timestamp() ELSE NULL END,
 next_run_at=clock_timestamp()+INTERVAL '15 seconds',lease_token='',lease_until=NULL
 WHERE id=$1 AND lease_token=$2 AND lease_until>clock_timestamp() AND delivered_at IS NULL`, event.ID, event.LeaseToken, delivered)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.ErrGenerationLeaseLost
	}
	return err
}
