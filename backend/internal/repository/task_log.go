package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
)

const taskLogColumns = `t.id,t.user_id,t.api_key_id,t.group_id,COALESCE(g.name,''),t.account_id,t.upstream_id,
 t.model,t.platform,t.protocol,t.action,t.plugin_key,t.plugin_version,t.status,t.delivery_status,t.billing_status,t.progress,
 t.hold_amount,t.actual_amount,t.error_code,t.error_message,t.created_at,t.updated_at,t.finished_at,t.source,
 COALESCE(p.monitor_status,''),COALESCE(p.last_error_code,'')`

const taskLogFrom = ` FROM sub2api_task_log_records t
 LEFT JOIN groups g ON g.id=t.group_id
 LEFT JOIN sub2api_native_video_polls p ON t.source='native' AND p.task_id=t.id`

const taskLogWhere = ` WHERE ($1::bigint=0 OR t.user_id=$1)
 AND ($2::bigint=0 OR t.group_id=$2) AND ($3::bigint=0 OR t.account_id=$3)
 AND ($4::text='' OR t.id=$4 OR (t.source='native' AND
 (t.upstream_id=$4 OR (t.protocol='ark_video' AND t.upstream_id='seedance:' || $4))))
 AND ($5::text='' OR t.model=$5)
 AND ($6::text='' OR t.plugin_key=$6) AND ($7::text='' OR t.status=$7)
 AND ($8::text='' OR t.billing_status=$8)
 AND ($9::timestamptz IS NULL OR t.created_at >= $9)
 AND ($10::timestamptz IS NULL OR t.created_at < $10)
 AND (NOT $11::boolean OR t.status='submission_uncertain' OR t.billing_status='review' OR p.monitor_status='review')`

func scanTaskLog(row interface{ Scan(...any) error }, fields bool) (*service.TaskLogDetail, error) {
	t := &service.TaskLogDetail{Events: []service.TaskLogEvent{}}
	args := []any{&t.ID, &t.UserID, &t.APIKeyID, &t.GroupID, &t.GroupName, &t.AccountID, &t.UpstreamID,
		&t.Model, &t.Platform, &t.Protocol, &t.Action, &t.PluginKey, &t.PluginVersion, &t.Status, &t.DeliveryStatus, &t.BillingStatus, &t.Progress,
		&t.HoldAmount, &t.ActualAmount, &t.ErrorCode, &t.ErrorMessage, &t.CreatedAt, &t.UpdatedAt, &t.FinishedAt, &t.Source, &t.MonitorStatus, &t.MonitorErrorCode}
	var raw []byte
	if fields {
		args = append(args, &raw)
	}
	if err := row.Scan(args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrGenerationTaskNotFound
		}
		return nil, err
	}
	if fields {
		if err := json.Unmarshal(raw, &t.Fields); err != nil {
			return nil, fmt.Errorf("decode public task fields: %w", err)
		}
	}
	return t, nil
}

func (r *generationTaskRepository) ListTaskLogs(ctx context.Context, f service.TaskLogFilter) ([]service.TaskLog, int64, error) {
	if f.Page < 1 || f.Page > 100000 || f.PageSize < 1 || f.PageSize > 100 || f.UserID < 0 || f.GroupID < 0 || f.AccountID < 0 {
		return nil, 0, errors.New("invalid task log filter")
	}
	args := []any{f.UserID, f.GroupID, f.AccountID, f.TaskID, f.Model, f.PluginKey, f.Status, f.BillingStatus, f.Start, f.End, f.ReviewOnly}
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*)`+taskLogFrom+taskLogWhere, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT `+taskLogColumns+taskLogFrom+taskLogWhere+` ORDER BY t.created_at DESC,t.id DESC LIMIT $12 OFFSET $13`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.TaskLog, 0)
	for rows.Next() {
		t, err := scanTaskLog(rows, false)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, t.TaskLog)
	}
	return items, total, rows.Err()
}

func (r *generationTaskRepository) GetTaskLog(ctx context.Context, id string, owner int64) (*service.TaskLogDetail, error) {
	if owner < 0 {
		return nil, service.ErrGenerationTaskNotFound
	}
	t, err := scanTaskLog(r.db.QueryRowContext(ctx, `SELECT `+taskLogColumns+`,t.public_fields`+taskLogFrom+` WHERE t.id=$1 AND ($2::bigint=0 OR t.user_id=$2)`, id, owner), true)
	if err != nil {
		return nil, err
	}
	if t.Source == "native" {
		return r.nativeVideoTaskEvents(ctx, t)
	}
	// Event payloads may contain prices, upstream IDs or private plugin state.
	// Only host-owned event names and timestamps are part of the console view.
	rows, err := r.db.QueryContext(ctx, `SELECT event_type,created_at FROM sub2api_generation_events WHERE task_id=$1 ORDER BY created_at,id LIMIT 500`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var event service.TaskLogEvent
		if err := rows.Scan(&event.Type, &event.CreatedAt); err != nil {
			return nil, err
		}
		t.Events = append(t.Events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if owner == 0 {
		if err := r.populateAdminTaskBilling(ctx, t); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (r *generationTaskRepository) populateAdminTaskBilling(ctx context.Context, t *service.TaskLogDetail) error {
	var priceRaw, sourceCostRaw []byte
	err := r.db.QueryRowContext(ctx, `SELECT price,source_cost FROM sub2api_generation_tasks WHERE id=$1`, t.ID).Scan(&priceRaw, &sourceCostRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var price generation.PriceSnapshot
	if len(priceRaw) == 0 || json.Unmarshal(priceRaw, &price) != nil || price.Rule.Mode == "" {
		return nil
	}
	mult := price.GroupMultiplier
	summary := &service.TaskLogBillingSummary{
		Mode:            price.Rule.Mode,
		GroupMultiplier: &mult,
	}
	if price.Expression != nil {
		summary.MatchedTier = price.Expression.EstimatedTier
		summary.RequestRules = slices.Clone(price.Expression.EstimatedRequestRules)
	}
	var sourceCost *generation.Cost
	if len(sourceCostRaw) > 0 && string(sourceCostRaw) != "null" {
		var parsed generation.Cost
		if json.Unmarshal(sourceCostRaw, &parsed) == nil && parsed.Source != "" {
			sourceCost = &parsed
		}
	}
	var settledPayload []byte
	err = r.db.QueryRowContext(ctx, `SELECT payload FROM sub2api_generation_events WHERE task_id=$1 AND event_key='settled' LIMIT 1`, t.ID).Scan(&settledPayload)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if len(settledPayload) > 0 {
		var settled struct {
			Expression      *generation.ExpressionCharge `json:"pricing_expression"`
			QuoteComparison *generation.QuoteComparison  `json:"quote_comparison"`
		}
		if json.Unmarshal(settledPayload, &settled) == nil {
			if settled.Expression != nil {
				if settled.Expression.Tier != "" {
					summary.MatchedTier = settled.Expression.Tier
				}
				if len(settled.Expression.RequestRules) > 0 {
					summary.RequestRules = slices.Clone(settled.Expression.RequestRules)
				}
			}
			if settled.QuoteComparison != nil {
				summary.QuoteComparison = settled.QuoteComparison
			}
		}
	}
	if summary.QuoteComparison == nil && price.Quote != nil {
		summary.QuoteComparison = price.CompareQuote(sourceCost, t.ActualAmount)
	}
	t.Billing = summary
	return nil
}

func (r *generationTaskRepository) nativeVideoTaskEvents(ctx context.Context, t *service.TaskLogDetail) (*service.TaskLogDetail, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT event_type,observed_at FROM (
 SELECT 'native_submitted' AS event_type,created_at AS observed_at,0 AS position FROM sub2api_native_video_tasks WHERE id=$1
 UNION ALL SELECT 'native_running',running_at,1 FROM sub2api_native_video_tasks WHERE id=$1 AND running_at IS NOT NULL
 UNION ALL SELECT 'native_' || status,finished_at,2 FROM sub2api_native_video_tasks WHERE id=$1 AND finished_at IS NOT NULL
 UNION ALL SELECT 'native_monitor_review',updated_at,3 FROM sub2api_native_video_polls WHERE task_id=$1 AND monitor_status='review'
 UNION ALL SELECT 'native_billing_' || status,updated_at,4 FROM sub2api_native_video_settlements WHERE task_id=$1 AND status IN('settled','logged','ignored','review')
 UNION ALL SELECT 'native_' || action,created_at,5 FROM sub2api_native_video_actions WHERE task_id=$1
 ) events ORDER BY observed_at,position LIMIT 500`, t.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var event service.TaskLogEvent
		if err := rows.Scan(&event.Type, &event.CreatedAt); err != nil {
			return nil, err
		}
		t.Events = append(t.Events, event)
	}
	return t, rows.Err()
}
