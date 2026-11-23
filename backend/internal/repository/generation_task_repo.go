package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/shopspring/decimal"
)

type generationTaskRepository struct{ db *sql.DB }

func NewGenerationTaskRepository(db *sql.DB) service.GenerationTaskRepository {
	return &generationTaskRepository{db: db}
}

// Native maintenance shares storage and financial facts, but has separate
// ownership and claim methods. Its assembly does not require the New API host.
func NewNativeVideoPollRepository(db *sql.DB) service.NativeVideoPollRepository {
	return &generationTaskRepository{db: db}
}

func NewNativeVideoSettlementRepository(db *sql.DB) service.NativeVideoSettlementRepository {
	return &generationTaskRepository{db: db}
}

const generationTaskColumns = `id,user_id,api_key_id,group_id,account_id,plugin_version_id,idempotency_key,fingerprint,
	 model,upstream_model,platform,protocol,action,request,config_encrypted,upstream_id,state,data,usage,price,
	status,delivery_status,billing_status,progress,error_code,error_message,created_at,updated_at,finished_at,next_run_at,lease_token,attempts,reconciled_amount,connection,funding,public_fields,files,request_plugins_encrypted,request_report_encrypted,origins,credential_binding,preparation_status,preparation_encrypted,public_deleted_at`

func scanGenerationTask(row interface{ Scan(...any) error }) (*service.GenerationTask, error) {
	t := &service.GenerationTask{}
	var price []byte
	var connection []byte
	var fundingRaw []byte
	var publicFields []byte
	var files, origins, credentialBinding []byte
	err := row.Scan(&t.ID, &t.UserID, &t.APIKeyID, &t.GroupID, &t.AccountID, &t.PluginVersionID, &t.IdempotencyKey, &t.Fingerprint,
		&t.Model, &t.UpstreamModel, &t.Platform, &t.Protocol, &t.Action, &t.Request, &t.ConfigEncrypted, &t.UpstreamID, &t.State, &t.Data, &t.Usage, &price,
		&t.Status, &t.DeliveryStatus, &t.BillingStatus, &t.Progress, &t.ErrorCode, &t.ErrorMessage, &t.CreatedAt, &t.UpdatedAt, &t.FinishedAt, &t.NextRunAt, &t.LeaseToken, &t.Attempts, &t.ReconciledAmount, &connection, &fundingRaw, &publicFields, &files, &t.RequestPluginsEncrypted, &t.RequestReportEncrypted, &origins, &credentialBinding, &t.PreparationStatus, &t.PreparationEncrypted, &t.PublicDeletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGenerationTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(price, &t.Price); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(connection, &t.Connection); err != nil {
		return nil, err
	}
	var funding generationFunding
	if err = json.Unmarshal(fundingRaw, &funding); err != nil {
		return nil, err
	}
	t.SubscriptionID = funding.Command.SubscriptionID
	if err = json.Unmarshal(publicFields, &t.PublicFields); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(files, &t.Files); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(origins, &t.Origins); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(credentialBinding, &t.CredentialBinding); err != nil || t.CredentialBinding.Validate() != nil {
		return nil, service.ErrGenerationCredential
	}
	return t, nil
}

type generationFunding struct {
	Command             service.UsageBillingCommand
	KeyWindows          [3]*time.Time
	SubscriptionWindows [3]*time.Time
	AccountDailyReset   *string
	AccountWeeklyReset  *string
}

func (r *generationTaskRepository) CreateHeld(ctx context.Context, t *service.GenerationTask, cmd *service.UsageBillingCommand) (*service.GenerationTask, error) {
	if err := service.ValidateGenerationTaskSnapshot(t); err != nil {
		return nil, err
	}
	if cmd == nil || cmd.UserID != t.UserID || cmd.APIKeyID != t.APIKeyID || cmd.AccountID != t.AccountID {
		return nil, errors.New("invalid generation funding identity")
	}
	command := *cmd
	command.Normalize()
	hold := t.Price.Hold
	if !decimal.NewFromFloat(command.APIKeyQuotaCost).Equal(hold) || !decimal.NewFromFloat(command.APIKeyRateLimitCost).Equal(hold) || command.AccountQuotaCost < 0 {
		return nil, errors.New("generation requires valid matching reservations")
	}
	if command.BalanceCost < 0 || command.SubscriptionCost < 0 || (command.SubscriptionCost > 0 && command.SubscriptionID == nil) {
		return nil, errors.New("invalid generation funding source")
	}
	if (command.BalanceCost > 0) == (command.SubscriptionCost > 0) && hold.IsPositive() {
		return nil, errors.New("generation requires exactly one funding source")
	}
	if !decimal.NewFromFloat(command.BalanceCost + command.SubscriptionCost).Equal(hold) {
		return nil, errors.New("generation funding must match hold")
	}
	price, err := json.Marshal(t.Price)
	if err != nil {
		return nil, err
	}
	connection, err := json.Marshal(t.Connection)
	if err != nil {
		return nil, err
	}
	publicFields, err := json.Marshal(t.PublicFields)
	if err != nil || len(publicFields) > 16384 {
		return nil, errors.New("invalid public video fields")
	}
	files, err := generationFileSnapshotJSON(t.Files)
	if err != nil {
		return nil, err
	}
	origins, err := generationOriginJSON(t.Origins)
	if err != nil {
		return nil, err
	}
	credentialBinding, err := json.Marshal(t.CredentialBinding)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var inserted string
	err = tx.QueryRowContext(ctx, `INSERT INTO sub2api_generation_tasks(id,user_id,api_key_id,group_id,account_id,plugin_version_id,idempotency_key,fingerprint,
 model,upstream_model,platform,protocol,action,request,config_encrypted,price,funding,hold_amount,connection,public_fields,files,request_plugins_encrypted,request_report_encrypted,request_text,origins,credential_binding,preparation_status)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,$15,$16::jsonb,'{}'::jsonb,$17,$18::jsonb,$19::jsonb,$20::jsonb,$21,$22,$23,$24::jsonb,$25::jsonb,$26)
 ON CONFLICT(api_key_id,idempotency_key) DO NOTHING RETURNING id`, t.ID, t.UserID, t.APIKeyID, t.GroupID, t.AccountID, t.PluginVersionID, t.IdempotencyKey, t.Fingerprint,
		t.Model, t.UpstreamModel, t.Platform, t.Protocol, t.Action, []byte(t.Request), t.ConfigEncrypted, price, hold.StringFixed(8), connection, publicFields, files, t.RequestPluginsEncrypted, t.RequestReportEncrypted, string(t.Request), origins, credentialBinding, t.PreparationStatus).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		existing, err := scanGenerationTask(tx.QueryRowContext(ctx, `SELECT `+prefixGenerationColumns("")+` FROM sub2api_generation_tasks WHERE api_key_id=$1 AND idempotency_key=$2 AND public_deleted_at IS NULL FOR SHARE`, t.APIKeyID, t.IdempotencyKey))
		if errors.Is(err, service.ErrGenerationTaskNotFound) {
			return nil, service.ErrGenerationIdempotencyConflict
		}
		if err != nil {
			return nil, err
		}
		if existing.UserID != t.UserID || existing.Fingerprint != t.Fingerprint || (!service.IsVideoGenerationProtocol(t.Protocol) || service.GenerationTaskSubmitPlugin(ctx) != "") && existing.GroupID != t.GroupID {
			return nil, service.ErrGenerationIdempotencyConflict
		}
		return existing, nil
	}
	if err != nil {
		return nil, err
	}
	if err = bindGenerationOrigins(ctx, tx, t); err != nil {
		return nil, err
	}
	if err = bindGenerationFiles(ctx, tx, t); err != nil {
		return nil, err
	}
	// Lock the same funding identities before checking or changing balances.
	var userStatus string
	var userConcurrency int
	if err = tx.QueryRowContext(ctx, `SELECT status,concurrency FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, t.UserID).Scan(&userStatus, &userConcurrency); err != nil {
		return nil, err
	}
	if userStatus != service.StatusActive {
		return nil, errors.New("user is disabled")
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sub2api_generation_tasks WHERE user_id=$1 AND status IN('queued','submitting','running','submission_uncertain')`, t.UserID).Scan(&active); err != nil {
		return nil, err
	}
	if active > max(userConcurrency, 1) {
		return nil, service.ErrGenerationCapacity
	}
	var keyOK bool
	err = tx.QueryRowContext(ctx, `SELECT status='active' AND user_id=$2 AND group_id=$3 AND (expires_at IS NULL OR expires_at>NOW())
 AND (quota<=0 OR quota_used+$4<=quota)
 AND (rate_limit_5h<=0 OR (CASE WHEN window_5h_start IS NULL OR window_5h_start+INTERVAL '5 hours'<=NOW() THEN 0 ELSE usage_5h END)+$4<=rate_limit_5h)
 AND (rate_limit_1d<=0 OR (CASE WHEN window_1d_start IS NULL OR window_1d_start+INTERVAL '24 hours'<=NOW() THEN 0 ELSE usage_1d END)+$4<=rate_limit_1d)
 AND (rate_limit_7d<=0 OR (CASE WHEN window_7d_start IS NULL OR window_7d_start+INTERVAL '7 days'<=NOW() THEN 0 ELSE usage_7d END)+$4<=rate_limit_7d)
 FROM api_keys WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, t.APIKeyID, t.UserID, t.GroupID, hold.StringFixed(8)).Scan(&keyOK)
	if err != nil {
		return nil, err
	}
	if !keyOK {
		return nil, errors.New("API key has insufficient quota for the task hold")
	}
	if command.SubscriptionID != nil {
		var subscriptionOK bool
		err = tx.QueryRowContext(ctx, `SELECT us.status='active' AND us.user_id=$2 AND us.group_id=$3 AND us.expires_at>NOW()
 AND (g.daily_limit_usd IS NULL OR us.daily_usage_usd+$4<=g.daily_limit_usd)
 AND (g.weekly_limit_usd IS NULL OR us.weekly_usage_usd+$4<=g.weekly_limit_usd)
 AND (g.monthly_limit_usd IS NULL OR us.monthly_usage_usd+$4<=g.monthly_limit_usd)
 FROM user_subscriptions us JOIN groups g ON g.id=us.group_id
 WHERE us.id=$1 AND us.deleted_at IS NULL AND g.deleted_at IS NULL FOR UPDATE OF us`, *command.SubscriptionID, t.UserID, t.GroupID, hold.StringFixed(8)).Scan(&subscriptionOK)
		if err != nil {
			return nil, err
		}
		if !subscriptionOK {
			return nil, errors.New("subscription has insufficient quota for the task hold")
		}
	}
	// Reserve user funds; key/subscription counters are provisional charges in
	// the admission windows, shared with all existing synchronous requests.
	if command.BalanceCost > 0 {
		if _, err = reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: t.UserID, HoldAmount: command.BalanceCost}); err != nil {
			return nil, err
		}
	}
	var accountActive bool
	var accountConcurrency int
	if err = tx.QueryRowContext(ctx, `SELECT status='active' AND schedulable,concurrency FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, t.AccountID).Scan(&accountActive, &accountConcurrency); err != nil {
		return nil, err
	}
	if !accountActive {
		if len(t.Origins) > 0 {
			return nil, service.ErrGenerationOrigin
		}
		return nil, errors.New("upstream account is disabled")
	}
	if err = validateGenerationTaskAccount(ctx, tx, t); err != nil {
		return nil, err
	}
	if err = bindGenerationCredential(ctx, tx, t); err != nil {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sub2api_generation_tasks WHERE account_id=$1 AND status IN('queued','submitting','running','submission_uncertain')`, t.AccountID).Scan(&active); err != nil {
		return nil, err
	}
	if active > max(accountConcurrency, 1) {
		return nil, service.ErrGenerationCapacity
	}
	provisional := command
	provisional.BalanceCost = 0
	provisionalResult := &service.UsageBillingApplyResult{}
	if err = (&usageBillingRepository{db: r.db}).applyUsageBillingEffects(ctx, tx, &provisional, provisionalResult); err != nil {
		return nil, err
	}
	if q := provisionalResult.QuotaState; q != nil {
		if q.TotalLimit > 0 && q.TotalUsed > q.TotalLimit || q.DailyLimit > 0 && q.DailyUsed > q.DailyLimit || q.WeeklyLimit > 0 && q.WeeklyUsed > q.WeeklyLimit {
			return nil, errors.New("upstream account quota is insufficient for the reservation")
		}
	}
	funding := generationFunding{Command: command}
	if command.AccountQuotaCost > 0 {
		if err = tx.QueryRowContext(ctx, `SELECT extra->>'quota_daily_start',extra->>'quota_weekly_start' FROM accounts WHERE id=$1`, t.AccountID).Scan(&funding.AccountDailyReset, &funding.AccountWeeklyReset); err != nil {
			return nil, err
		}
	}
	if err = tx.QueryRowContext(ctx, `SELECT window_5h_start,window_1d_start,window_7d_start FROM api_keys WHERE id=$1`, t.APIKeyID).Scan(&funding.KeyWindows[0], &funding.KeyWindows[1], &funding.KeyWindows[2]); err != nil {
		return nil, err
	}
	if command.SubscriptionID != nil {
		if err = tx.QueryRowContext(ctx, `SELECT daily_window_start,weekly_window_start,monthly_window_start FROM user_subscriptions WHERE id=$1`, *command.SubscriptionID).Scan(&funding.SubscriptionWindows[0], &funding.SubscriptionWindows[1], &funding.SubscriptionWindows[2]); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(funding)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sub2api_generation_tasks SET funding=$2::jsonb WHERE id=$1`, t.ID, raw); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sub2api_generation_events(task_id,event_key,event_type,payload) VALUES($1,'held','funds_held',$2::jsonb)`, t.ID, price); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetOwned(ctx, t.ID, t.UserID, t.APIKeyID)
}

func (r *generationTaskRepository) GetOwned(ctx context.Context, id string, user, key int64) (*service.GenerationTask, error) {
	return scanGenerationTask(r.db.QueryRowContext(ctx, `SELECT `+prefixGenerationColumns("")+` FROM sub2api_generation_tasks WHERE id=$1 AND user_id=$2 AND api_key_id=$3 AND public_deleted_at IS NULL`, id, user, key))
}
func (r *generationTaskRepository) FindIdempotent(ctx context.Context, user, key int64, idempotency string) (*service.GenerationTask, error) {
	// The original key cannot create a second paid task after deletion.
	task, err := scanGenerationTask(r.db.QueryRowContext(ctx, `SELECT `+prefixGenerationColumns("")+` FROM sub2api_generation_tasks WHERE user_id=$1 AND api_key_id=$2 AND idempotency_key=$3`, user, key, idempotency))
	if err == nil && task.PublicDeletedAt != nil {
		return nil, service.ErrGenerationIdempotencyConflict
	}
	return task, err
}
func (r *generationTaskRepository) ListOwned(ctx context.Context, user, key int64, after string, limit int, order string) ([]service.GenerationTask, error) {
	if limit < 1 || limit > 101 {
		return nil, errors.New("invalid generation list limit")
	}
	comparison, direction := "<", "DESC"
	if order == "asc" {
		comparison, direction = ">", "ASC"
	} else if order != "desc" {
		return nil, errors.New("invalid generation list order")
	}
	// Both fragments are selected from constants; cursor and owner are bound.
	const videoOnly = `(protocol='' OR protocol IN ('openai_video','xai_video','xai_video_edit','xai_video_extension','ark_video'))`
	rows, err := r.db.QueryContext(ctx, `SELECT `+prefixGenerationColumns("")+` FROM sub2api_generation_tasks WHERE user_id=$1 AND api_key_id=$2 AND public_deleted_at IS NULL AND `+videoOnly+` AND ($3='' OR (created_at,id)`+comparison+`(SELECT created_at,id FROM sub2api_generation_tasks WHERE id=$3 AND user_id=$1 AND api_key_id=$2 AND `+videoOnly+`)) ORDER BY created_at `+direction+`,id `+direction+` LIMIT $4`, user, key, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]service.GenerationTask, 0)
	for rows.Next() {
		t, err := scanGenerationTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *t)
	}
	return result, rows.Err()
}
func (r *generationTaskRepository) Claim(ctx context.Context, token string, ttl time.Duration) (*service.GenerationTask, error) {
	if token == "" || len(token) > 64 || ttl < time.Minute || ttl > 5*time.Minute {
		return nil, errors.New("invalid generation lease")
	}
	return scanGenerationTask(r.db.QueryRowContext(ctx, `WITH candidate AS (
 SELECT id FROM sub2api_generation_tasks WHERE next_run_at<=NOW() AND (lease_until IS NULL OR lease_until<NOW())
 AND billing_status IN('held','pending') AND status<>'submission_uncertain' ORDER BY next_run_at,id FOR UPDATE SKIP LOCKED LIMIT 1
 ) UPDATE sub2api_generation_tasks t SET lease_token=$1,lease_until=NOW()+$2*INTERVAL '1 second',attempts=attempts+1,
 status=CASE WHEN t.status='queued' THEN 'submitting' WHEN t.status='submitting' AND t.preparation_status<>'pending' THEN 'submission_uncertain' ELSE t.status END,
 updated_at=NOW() FROM candidate c WHERE t.id=c.id RETURNING `+prefixGenerationColumns("t"), token, int(ttl.Seconds())))
}

func prefixGenerationColumns(prefix string) string {
	// Column names and the prefix are host-owned, never caller input.
	if prefix != "" {
		prefix += "."
	}
	result := ""
	for _, column := range strings.Split(generationTaskColumns, ",") {
		if result != "" {
			result += ","
		}
		column = strings.TrimSpace(column)
		if column == "request" {
			result += "COALESCE(" + prefix + "request_text," + prefix + "request::text)::json AS request"
		} else {
			result += prefix + column
		}
	}
	return result
}

func (r *generationTaskRepository) SaveProgress(ctx context.Context, t *service.GenerationTask, delay time.Duration) error {
	if delay < time.Second || delay > time.Hour {
		return errors.New("invalid generation retry delay")
	}
	for _, raw := range []json.RawMessage{t.State, t.Data, t.Usage} {
		if !json.Valid(raw) || len(raw) > 1<<20 {
			return errors.New("invalid task plugin snapshot")
		}
	}
	result, err := r.db.ExecContext(ctx, `UPDATE sub2api_generation_tasks SET upstream_id=$3,state=$4::jsonb,data=$5::jsonb,usage=$6::jsonb,
 status=$7,delivery_status=$8,billing_status=$9,progress=$10,error_code=$11,error_message=$12,
	 finished_at=COALESCE(finished_at,$13),next_run_at=NOW()+$14*INTERVAL '1 second',lease_until=NULL,lease_token='',updated_at=NOW(),request_report_encrypted=$15,
	 preparation_status=$16
 WHERE id=$1 AND lease_token=$2 AND lease_until>NOW() AND billing_status<>'settled'`, t.ID, t.LeaseToken, t.UpstreamID, []byte(t.State), []byte(t.Data), []byte(t.Usage), t.Status, t.DeliveryStatus, t.BillingStatus, t.Progress, t.ErrorCode, t.ErrorMessage, t.FinishedAt, int(delay.Seconds()), t.RequestReportEncrypted, t.PreparationStatus)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.ErrGenerationLeaseLost
	}
	return err
}

func (r *generationTaskRepository) Settle(ctx context.Context, t *service.GenerationTask, actual decimal.Decimal, cost *service.GenerationCost) error {
	if actual.IsNegative() || actual.Exponent() < -service.UsageBillingMonetaryScale {
		return errors.New("invalid task settlement amount")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var fundingRaw []byte
	var hold decimal.Decimal
	var status, billing, lease string
	var errorCode string
	var userID, keyID int64
	var leaseUntil sql.NullTime
	var previous sql.NullString
	var persistedPrice, persistedUsage []byte
	var reconciled sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT funding,hold_amount,status,billing_status,lease_token,actual_amount::text,user_id,api_key_id,lease_until,price,usage,reconciled_amount::text,error_code FROM sub2api_generation_tasks WHERE id=$1 FOR UPDATE`, t.ID).Scan(&fundingRaw, &hold, &status, &billing, &lease, &previous, &userID, &keyID, &leaseUntil, &persistedPrice, &persistedUsage, &reconciled, &errorCode)
	if err != nil {
		return err
	}
	if billing == service.GenerationBillingSettled {
		if previous.Valid && previous.String == actual.StringFixed(8) {
			return nil
		}
		return service.ErrUsageBillingRequestConflict
	}
	if lease != t.LeaseToken || lease == "" || !leaseUntil.Valid || !leaseUntil.Time.After(time.Now()) {
		return service.ErrGenerationLeaseLost
	}
	if status != service.GenerationSucceeded && status != service.GenerationFailed {
		return errors.New("task is not terminal")
	}
	if (errorCode == service.GenerationCanceledBeforeSubmit || errorCode == service.GenerationCanceledUpstream) && (!actual.IsZero() || cost != nil) {
		return service.ErrUsageBillingRequestConflict
	}
	if actual.GreaterThan(hold) {
		return service.ErrGenerationPriceExceedsHold
	}
	// Financial effects use the immutable stored expression and separately
	// persisted completion facts, never a caller-supplied mutable price object.
	var price service.GenerationPriceSnapshot
	if err = json.Unmarshal(persistedPrice, &price); err != nil {
		return err
	}
	var expressionCharge *service.GenerationExpressionCharge
	if (price.Rule.Mode == "expression" || price.Rule.UsesFinalBill()) && reconciled.Valid {
		confirmed, parseErr := decimal.NewFromString(reconciled.String)
		if parseErr != nil || !actual.Equal(confirmed) {
			return service.ErrUsageBillingRequestConflict
		}
	} else if (price.Rule.Mode == "expression" || price.Rule.UsesFinalBill()) && status == service.GenerationFailed && !actual.IsZero() {
		return service.ErrUsageBillingRequestConflict
	}
	if price.Rule.Mode == "expression" && status == service.GenerationSucceeded && !reconciled.Valid {
		var facts map[string]json.RawMessage
		if json.Unmarshal(persistedUsage, &facts) != nil {
			return service.ErrGenerationCostPending
		}
		expressionCharge, err = price.SettleExpression(facts["_host_final_usage"])
		if err != nil {
			return err
		}
		if !actual.Equal(expressionCharge.Amount) {
			return service.ErrUsageBillingRequestConflict
		}
	}
	var finalBillBase *decimal.Decimal
	if price.Rule.UsesFinalBill() && status == service.GenerationSucceeded && !reconciled.Valid {
		var facts map[string]json.RawMessage
		if json.Unmarshal(persistedUsage, &facts) != nil {
			return service.ErrGenerationCostPending
		}
		observed := generation.ObservedCost(facts["_host_final_usage"])
		base, deriveErr := price.FinalBillBase(observed)
		if deriveErr != nil {
			return deriveErr
		}
		verified, deriveErr := price.Settle(price.Quantity, observed)
		if deriveErr != nil {
			return deriveErr
		}
		if !actual.Equal(verified) {
			return service.ErrUsageBillingRequestConflict
		}
		finalBillBase, cost = &base, observed
	}
	if price.Rule.Mode == "cost_plus" && !price.Rule.UsesFinalBill() && status == service.GenerationSucceeded && !reconciled.Valid {
		var facts map[string]json.RawMessage
		if json.Unmarshal(persistedUsage, &facts) == nil {
			if observed := generation.ObservedCost(facts["_host_final_usage"]); observed != nil {
				verified, deriveErr := price.Settle(price.Quantity, observed)
				if deriveErr != nil {
					return deriveErr
				}
				if !actual.Equal(verified) {
					return service.ErrUsageBillingRequestConflict
				}
				cost = observed
			}
		}
	}
	var funding generationFunding
	if err = json.Unmarshal(fundingRaw, &funding); err != nil {
		return err
	}
	refund := hold.Sub(actual)
	cmd := funding.Command
	if cmd.UserID != userID || cmd.APIKeyID != keyID {
		return errors.New("persisted generation funding identity mismatch")
	}
	if cmd.BalanceCost > 0 {
		_, err = captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: userID, HoldAmount: hold.InexactFloat64(), ActualAmount: actual.InexactFloat64()})
		if err != nil {
			return err
		}
	}
	accountRefund := decimal.Zero
	if cmd.AccountQuotaCost > 0 {
		var measuredBase *decimal.Decimal
		if expressionCharge != nil {
			measuredBase = &expressionCharge.BaseAmount
		} else if finalBillBase != nil {
			measuredBase = finalBillBase
		}
		if measuredBase != nil {
			// A zero user/group multiplier must still settle the account's
			// independent quota by actual usage, rather than the maximum hold.
			accountActual := decimal.NewFromFloat(cmd.AccountQuotaCost).Mul(*measuredBase).Div(*price.Rule.Maximum).Round(service.UsageBillingMonetaryScale)
			accountRefund = decimal.NewFromFloat(cmd.AccountQuotaCost).Sub(accountActual)
		} else if hold.IsPositive() {
			accountRefund = decimal.NewFromFloat(cmd.AccountQuotaCost).Mul(refund).Div(hold).Round(8)
		} else if status == service.GenerationFailed {
			accountRefund = decimal.NewFromFloat(cmd.AccountQuotaCost)
		}
	}
	if accountRefund.IsPositive() {
		_, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=COALESCE(extra,'{}'::jsonb)||jsonb_build_object('quota_used',GREATEST(0,COALESCE((extra->>'quota_used')::numeric,0)-$2))
 ||CASE WHEN extra ? 'quota_daily_used' AND extra->>'quota_daily_start' IS NOT DISTINCT FROM $3::text THEN jsonb_build_object('quota_daily_used',GREATEST(0,COALESCE((extra->>'quota_daily_used')::numeric,0)-$2)) ELSE '{}'::jsonb END
 ||CASE WHEN extra ? 'quota_weekly_used' AND extra->>'quota_weekly_start' IS NOT DISTINCT FROM $4::text THEN jsonb_build_object('quota_weekly_used',GREATEST(0,COALESCE((extra->>'quota_weekly_used')::numeric,0)-$2)) ELSE '{}'::jsonb END,
 updated_at=NOW() WHERE id=$1`, cmd.AccountID, accountRefund.StringFixed(8), funding.AccountDailyReset, funding.AccountWeeklyReset)
		if err != nil {
			return err
		}
	}
	// Return unused provisional charges only to the windows originally
	// reserved. A window reset cannot create credits in a new billing period.
	if refund.IsPositive() {
		_, err = tx.ExecContext(ctx, `UPDATE api_keys SET quota_used=GREATEST(0,quota_used-$2),
 usage_5h=CASE WHEN window_5h_start IS NOT DISTINCT FROM $3::timestamptz THEN GREATEST(0,usage_5h-$2) ELSE usage_5h END,
 usage_1d=CASE WHEN window_1d_start IS NOT DISTINCT FROM $4::timestamptz THEN GREATEST(0,usage_1d-$2) ELSE usage_1d END,
 usage_7d=CASE WHEN window_7d_start IS NOT DISTINCT FROM $5::timestamptz THEN GREATEST(0,usage_7d-$2) ELSE usage_7d END,
 status=CASE WHEN status='quota_exhausted' AND (quota<=0 OR quota_used-$2<quota) THEN 'active' ELSE status END,updated_at=NOW() WHERE id=$1`, keyID, refund.StringFixed(8), funding.KeyWindows[0], funding.KeyWindows[1], funding.KeyWindows[2])
		if err != nil {
			return err
		}
		if cmd.SubscriptionID != nil {
			_, err = tx.ExecContext(ctx, `UPDATE user_subscriptions SET
 daily_usage_usd=CASE WHEN daily_window_start IS NOT DISTINCT FROM $3::timestamptz THEN GREATEST(0,daily_usage_usd-$2) ELSE daily_usage_usd END,
 weekly_usage_usd=CASE WHEN weekly_window_start IS NOT DISTINCT FROM $4::timestamptz THEN GREATEST(0,weekly_usage_usd-$2) ELSE weekly_usage_usd END,
 monthly_usage_usd=CASE WHEN monthly_window_start IS NOT DISTINCT FROM $5::timestamptz THEN GREATEST(0,monthly_usage_usd-$2) ELSE monthly_usage_usd END,
 updated_at=NOW() WHERE id=$1`, *cmd.SubscriptionID, refund.StringFixed(8), funding.SubscriptionWindows[0], funding.SubscriptionWindows[1], funding.SubscriptionWindows[2])
			if err != nil {
				return err
			}
		}
	}
	source, err := json.Marshal(cost)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sub2api_generation_tasks SET billing_status='settled',actual_amount=$2,source_cost=$3::jsonb,lease_until=NULL,lease_token='',updated_at=NOW(),
 preparation_encrypted=CASE WHEN preparation_status='ready' THEN '' ELSE preparation_encrypted END,
 preparation_status=CASE WHEN preparation_status='ready' THEN 'cleaned' ELSE preparation_status END
 WHERE id=$1`, t.ID, actual.StringFixed(8), source); err != nil {
		return err
	}
	eventData := map[string]any{"actual_amount": actual, "hold_amount": hold, "source_cost": cost}
	if expressionCharge != nil {
		eventData["pricing_expression"] = expressionCharge
	}
	if comparison := price.CompareQuote(cost, &actual); comparison != nil {
		eventData["quote_comparison"] = comparison
	}
	event, _ := json.Marshal(eventData)
	if _, err = tx.ExecContext(ctx, `INSERT INTO sub2api_generation_events(task_id,event_key,event_type,payload) VALUES($1,'settled','funds_settled',$2::jsonb)`, t.ID, event); err != nil {
		return err
	}
	// The ordinary usage ledger and durable task settlement commit together.
	// Dashboard/account statistics therefore cannot miss a captured charge.
	accountActual := decimal.NewFromFloat(cmd.AccountQuotaCost).Sub(accountRefund)
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_logs(user_id,api_key_id,account_id,group_id,subscription_id,request_id,model,requested_model,upstream_model,
 total_cost,actual_cost,account_stats_cost,account_rate_multiplier,rate_multiplier,billing_type,billing_mode,request_type,video_count,video_duration_seconds,output_tokens,duration_ms,created_at)
 SELECT user_id,api_key_id,account_id,group_id,$3,id,model,model,upstream_model,
 CASE WHEN (price->>'group_multiplier')::numeric>0 THEN $2::numeric/(price->>'group_multiplier')::numeric ELSE 0 END,
 $2,$4,1,(price->>'group_multiplier')::numeric,$5,CASE WHEN (protocol='' OR protocol IN ('openai_video','xai_video','xai_video_edit','xai_video_extension','ark_video')) THEN 'video' ELSE 'per_request' END,1,CASE WHEN (protocol='' OR protocol IN ('openai_video','xai_video','xai_video_edit','xai_video_extension','ark_video')) AND status='succeeded' AND delivery_status='ready' THEN 1 ELSE 0 END,
 CASE WHEN jsonb_typeof(usage->'seconds')='number' THEN LEAST(GREATEST((usage->>'seconds')::numeric,0),3600)::integer ELSE NULL END,
 CASE WHEN jsonb_typeof(usage->'tokens')='number' THEN LEAST(GREATEST((usage->>'tokens')::numeric,0),2147483647)::integer ELSE 0 END,
 LEAST(GREATEST(EXTRACT(EPOCH FROM(COALESCE(finished_at,NOW())-created_at))*1000,0),2147483647)::integer,NOW()
 FROM sub2api_generation_tasks WHERE id=$1`, t.ID, actual.StringFixed(8), cmd.SubscriptionID, accountActual.StringFixed(8), func() int {
		if cmd.SubscriptionID != nil {
			return 1
		}
		return 0
	}())
	if err != nil {
		return err
	}
	if cmd.SubscriptionID == nil {
		if err := applyGenerationQuotaTx(ctx, tx, t.ID, userID, actual); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	t.BillingStatus = service.GenerationBillingSettled
	if t.PreparationStatus == "ready" {
		t.PreparationStatus, t.PreparationEncrypted = "cleaned", ""
	}
	return nil
}

func (r *generationTaskRepository) ListReview(ctx context.Context, limit int) ([]service.GenerationTask, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid review list limit")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+prefixGenerationColumns("")+` FROM sub2api_generation_tasks WHERE status='submission_uncertain' OR billing_status='review' ORDER BY created_at,id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]service.GenerationTask, 0)
	for rows.Next() {
		task, err := scanGenerationTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *task)
	}
	return result, rows.Err()
}

func (r *generationTaskRepository) Reconcile(ctx context.Context, id string, actor int64, action, upstreamID, reason string, amount *decimal.Decimal) error {
	if actor <= 0 || len(reason) < 4 || len(reason) > 2000 || len(upstreamID) > 512 {
		return errors.New("invalid task reconciliation")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	task, err := scanGenerationTask(tx.QueryRowContext(ctx, `SELECT `+prefixGenerationColumns("")+` FROM sub2api_generation_tasks WHERE id=$1 AND (lease_until IS NULL OR lease_until<NOW()) FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if task.Status != service.GenerationUncertain && task.BillingStatus != service.GenerationBillingReview {
		return errors.New("task does not require review")
	}
	state, billing := task.Status, service.GenerationBillingHeld
	var settled any
	switch action {
	case "resume_poll":
		if task.Status == service.GenerationSucceeded || task.Status == service.GenerationFailed {
			return errors.New("a completed task cannot resume polling")
		}
		if upstreamID == "" {
			return errors.New("an upstream task ID is required")
		}
		state = service.GenerationRunning
	case "refund":
		state = service.GenerationFailed
		billing = service.GenerationBillingPending
		settled = "0"
	case "settle":
		if task.Status != service.GenerationSucceeded && task.Status != service.GenerationFailed {
			return errors.New("confirm task completion before settlement")
		}
		if amount == nil || amount.IsNegative() || amount.GreaterThan(task.Price.Hold) || amount.Exponent() < -8 {
			return errors.New("settlement must be within the held amount")
		}
		billing = service.GenerationBillingPending
		settled = amount.StringFixed(8)
	default:
		return errors.New("unsupported reconciliation action")
	}
	if upstreamID == "" {
		upstreamID = task.UpstreamID
	}
	_, err = tx.ExecContext(ctx, `UPDATE sub2api_generation_tasks SET status=$2::varchar,billing_status=$3,upstream_id=$4,reconciled_amount=$5,reviewed_by=$6,review_reason=$7,
	 finished_at=CASE WHEN $2::varchar='failed' THEN COALESCE(finished_at,NOW()) ELSE finished_at END,
	 next_run_at=NOW(),lease_token='',lease_until=NULL,
	 error_code=CASE WHEN $2::varchar='failed' THEN 'generation_reconciled_refund' ELSE '' END,
	 error_message=CASE WHEN $2::varchar='failed' THEN 'The task was closed after administrator reconciliation.' ELSE '' END,updated_at=NOW() WHERE id=$1`, id, state, billing, upstreamID, settled, actor, reason)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"actor": actor, "action": action, "reason": reason, "amount": settled, "upstream_id": upstreamID})
	_, err = tx.ExecContext(ctx, `INSERT INTO sub2api_generation_events(task_id,event_key,event_type,payload) VALUES($1,'review:'||gen_random_uuid()::text,'administrator_reconciliation',$2::jsonb)`, id, payload)
	if err != nil {
		return err
	}
	return tx.Commit()
}
