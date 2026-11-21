package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const NewAPIVideoPricingKey = "newapi_video_pricing"

type GenerationSubmissionError struct {
	Uncertain bool
	Cause     error
}

func (e *GenerationSubmissionError) Error() string { return e.Cause.Error() }
func (e *GenerationSubmissionError) Unwrap() error { return e.Cause }

// GenerationService owns a bounded durable executor. No request or gin context
// crosses this boundary. A lost submitting lease requires reconciliation.
type GenerationService struct {
	repo     GenerationTaskRepository
	plugins  *NewAPIPluginService
	accounts AccountRepository
	gateway  *OpenAIGatewayService
	cache    *BillingCacheService
	keys     *APIKeyService
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	once     sync.Once
}

func NewGenerationService(repo GenerationTaskRepository, plugins *NewAPIPluginService, accounts AccountRepository, gateway *OpenAIGatewayService, cache *BillingCacheService, keys *APIKeyService) *GenerationService {
	s := &GenerationService{repo: repo, plugins: plugins, accounts: accounts, gateway: gateway, cache: cache, keys: keys}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	for range 2 {
		s.wg.Add(1)
		go s.work(ctx)
	}
	return s
}
func (s *GenerationService) Stop() {
	if s != nil {
		s.once.Do(func() { s.cancel(); s.wg.Wait() })
	}
}

func (s *GenerationService) work(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		callCtx, cancel := context.WithTimeout(ctx, 100*time.Second)
		task, err := s.repo.Claim(callCtx, uuid.NewString(), 2*time.Minute)
		if err == nil {
			err = s.process(callCtx, task)
		}
		cancel()
		if err != nil && !errors.Is(err, ErrGenerationTaskNotFound) && !errors.Is(err, context.Canceled) {
			// Hook errors may contain upstream payloads or credentials echoed by
			// third-party source. Persist only host-owned codes on the task.
			slog.Warn("generation_worker", "error_type", fmt.Sprintf("%T", err))
		}
	}
}

func (s *GenerationService) process(ctx context.Context, task *GenerationTask) error {
	if task.Status == GenerationUncertain {
		return s.repo.SaveProgress(ctx, task, time.Hour)
	}
	awaitingBill := generationBillingPending(task)
	if (task.Status == GenerationSucceeded || task.Status == GenerationFailed) && !awaitingBill {
		return s.settle(ctx, task)
	}
	if awaitingBill && task.FinishedAt != nil && time.Since(*task.FinishedAt) > 2*time.Hour {
		task.BillingStatus = GenerationBillingReview
		task.ErrorCode = "upstream_billing_timeout"
		task.ErrorMessage = "The video is complete but the upstream final bill requires administrator review."
		return s.repo.SaveProgress(ctx, task, time.Hour)
	}
	if !awaitingBill && time.Since(task.CreatedAt) > time.Hour {
		if task.Status == GenerationSubmitting && task.UpstreamID == "" {
			task.Status = GenerationFailed
			task.BillingStatus = GenerationBillingPending
			now := time.Now().UTC()
			task.FinishedAt = &now
			task.ErrorCode = "queue_expired"
			task.ErrorMessage = "The task expired before submission."
		} else {
			task.BillingStatus = GenerationBillingReview
			task.ErrorCode = "generation_timeout"
			task.ErrorMessage = "The upstream task is taking too long and requires reconciliation."
		}
		return s.repo.SaveProgress(ctx, task, time.Hour)
	}
	account, err := s.accounts.GetByID(ctx, task.AccountID)
	if err != nil {
		task.BillingStatus = GenerationBillingReview
		task.ErrorCode = "account_unavailable"
		task.ErrorMessage = "The upstream connection requires administrator review."
		return s.repo.SaveProgress(ctx, task, time.Hour)
	}
	if s.gateway.concurrencyService == nil {
		return errors.New("generation concurrency service unavailable")
	}
	slot, err := s.gateway.concurrencyService.AcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil || !slot.Acquired { // Not submitted: safely return it to the durable queue.
		if task.Status == GenerationSubmitting {
			task.Status = GenerationQueued
		}
		return s.repo.SaveProgress(ctx, task, 5*time.Second)
	}
	defer slot.ReleaseFunc()
	if task.Status == GenerationSubmitting {
		if !account.IsSchedulable() {
			task.Status = GenerationQueued
			return s.repo.SaveProgress(ctx, task, time.Minute)
		}
		err = s.plugins.SubmitTask(ctx, account, task)
		if err != nil {
			var submission *GenerationSubmissionError
			if errors.As(err, &submission) && !submission.Uncertain {
				task.Status = GenerationFailed
				task.BillingStatus = GenerationBillingPending
				task.ErrorCode = "upstream_rejected"
				task.ErrorMessage = "The upstream rejected this video request."
				now := time.Now().UTC()
				task.FinishedAt = &now
			} else {
				task.Status = GenerationUncertain
				task.BillingStatus = GenerationBillingReview
				task.ErrorCode = "submission_uncertain"
				task.ErrorMessage = "The upstream submission requires reconciliation; it will not be submitted twice."
			}
		}
	} else {
		err = s.plugins.PollTask(ctx, account, task)
		if err != nil {
			task.ErrorCode = "poll_retrying"
			task.ErrorMessage = "The upstream status is temporarily unavailable."
		} else if task.ErrorCode == "poll_retrying" {
			task.ErrorCode = ""
			task.ErrorMessage = ""
		}
	}
	// Persist terminal facts before financial effects. A crash resumes settlement.
	if saveErr := s.repo.SaveProgress(ctx, task, 10*time.Second); saveErr != nil {
		return saveErr
	}
	return err
}

func (s *GenerationService) settle(ctx context.Context, task *GenerationTask) error {
	if task.BillingStatus == GenerationBillingSettled {
		return nil
	}
	quantity := task.Price.Quantity
	unit := task.Price.Rule.Unit
	if task.Price.Rule.Mode == "cost_plus" {
		unit = task.Price.Rule.SourceUnit
	}
	if value, ok := generationQuantity(task.Usage, unit); ok {
		quantity = value
	}
	var finalFacts map[string]json.RawMessage
	_ = json.Unmarshal(task.Usage, &finalFacts)
	finalQuantity, observed := generationQuantity(finalFacts["_host_final_usage"], unit)
	if observed {
		quantity = finalQuantity
	}
	var cost *GenerationCost
	if task.Price.Rule.Mode == "cost_plus" && unit == "upstream_cost" {
		cost = generationObservedCost(finalFacts["_host_final_usage"])
		observed = cost != nil
	} else if task.Price.Rule.Mode == "cost_plus" && task.Price.Rule.SourceUnitPrice != nil {
		cost = &GenerationCost{Amount: quantity.Mul(*task.Price.Rule.SourceUnitPrice), Currency: task.Price.Rule.SourceCurrency, Unit: unit, Final: observed, Source: "configured_upstream_price", ObservedAt: time.Now().UTC()}
	}
	actual, err := task.Price.Settle(quantity, cost)
	if !observed {
		err = ErrGenerationCostPending
	}
	// FAILURE means the driver confirmed a non-billable failure. Drivers such as
	// xAI represent charged moderation as SUCCESS with a delivery notice.
	if task.Status == GenerationFailed {
		actual = decimal.Zero
		err = nil
	}
	if task.ReconciledAmount != nil {
		actual = *task.ReconciledAmount
		err = nil
	}
	if err != nil {
		task.BillingStatus = GenerationBillingReview
		task.ErrorCode = "billing_review"
		task.ErrorMessage = "Final usage requires administrator review."
		return s.repo.SaveProgress(ctx, task, time.Hour)
	}
	if err = s.repo.Settle(ctx, task, actual, cost); err != nil {
		return err
	}
	// Follow the ordinary gateway's platform-quota path, including subscription
	// exemption and inline DB persistence when Redis snapshot flushing is off.
	if task.BillingStatus == GenerationBillingSettled && task.SubscriptionID == nil && actual.IsPositive() && s.cache != nil && s.gateway.userPlatformQuotaRepo != nil && s.cache.HasUserPlatformQuotaLimit(ctx, task.UserID, task.Platform) {
		s.cache.IncrementUserPlatformQuotaUsage(task.UserID, task.Platform, actual.InexactFloat64())
		if s.gateway.cfg == nil || !s.gateway.cfg.Database.UserPlatformQuotaFlusherEnabled {
			persistPostUsagePlatformQuota(ctx, s.gateway.userPlatformQuotaRepo, task.UserID, task.Platform, actual.InexactFloat64())
		}
	}
	s.invalidate(ctx, task.UserID, task.GroupID, task.APIKeyID)
	return nil
}

func (s *GenerationService) invalidate(ctx context.Context, user, group, key int64) {
	if s.cache != nil {
		_ = s.cache.InvalidateUserBalance(ctx, user)
		_ = s.cache.InvalidateSubscription(ctx, user, group)
		_ = s.cache.InvalidateAPIKeyRateLimit(ctx, key)
	}
	if s.keys != nil {
		s.keys.InvalidateAuthCacheByUserID(ctx, user)
	}
}

func mergeGenerationFacts(initial, final json.RawMessage) json.RawMessage {
	values := map[string]any{}
	_ = json.Unmarshal(initial, &values)
	var updates map[string]any
	_ = json.Unmarshal(final, &updates)
	for key, value := range updates {
		values[key] = value
	}
	raw, _ := json.Marshal(values)
	return raw
}

func generationQuantity(facts json.RawMessage, unit string) (decimal.Decimal, bool) {
	if unit == "request" || unit == "upstream_cost" {
		return decimal.NewFromInt(1), true
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(facts, &values) != nil {
		return decimal.Zero, false
	}
	key := map[string]string{"second": "seconds", "token": "tokens", "credit": "credits"}[unit]
	if key == "" || len(values[key]) == 0 {
		return decimal.Zero, false
	}
	var number decimal.Decimal
	if json.Unmarshal(values[key], &number) != nil || number.IsNegative() {
		return decimal.Zero, false
	}
	return number, true
}

// Billing readiness and monetary facts are separate from media readiness.
// A completed video remains downloadable while the same upstream ID is polled.
func generationBillingPending(task *GenerationTask) bool {
	if task.ReconciledAmount != nil || task.Status != GenerationSucceeded {
		return false
	}
	var facts struct {
		Final struct {
			Pending bool `json:"billing_pending"`
		} `json:"_host_final_usage"`
	}
	_ = json.Unmarshal(task.Usage, &facts)
	return facts.Final.Pending
}

func generationObservedCost(raw json.RawMessage) *GenerationCost {
	var facts struct {
		Cost *GenerationCost `json:"upstream_cost"`
	}
	if json.Unmarshal(raw, &facts) != nil || facts.Cost == nil || !facts.Cost.Final || facts.Cost.Amount.IsNegative() || facts.Cost.Source == "" {
		return nil
	}
	facts.Cost.ObservedAt = time.Now().UTC()
	return facts.Cost
}

func generationRule(account *Account, group *Group, model, resolution string) (GenerationPriceRule, error) {
	var rules map[string]GenerationPriceRule
	if configured := account.Credentials[NewAPIVideoPricingKey]; configured != nil {
		raw, err := json.Marshal(configured)
		if err != nil || len(raw) > 64<<10 {
			return GenerationPriceRule{}, errors.New("invalid video pricing configuration")
		}
		if err = json.Unmarshal(raw, &rules); err != nil {
			return GenerationPriceRule{}, err
		}
		if rule, ok := rules[model]; ok {
			return rule, rule.Validate()
		}
		if rule, ok := rules["*"]; ok {
			return rule, rule.Validate()
		}
	}
	if group != nil {
		if price := group.GetVideoPriceForModel(model, resolution); price != nil {
			value := decimal.NewFromFloat(*price)
			return GenerationPriceRule{Mode: "fixed", Unit: "second", UnitPrice: &value}, nil
		}
	}
	return GenerationPriceRule{}, ErrGenerationPriceUnavailable
}

// Create snapshots a decoded protocol request, driver usage facts, and price.
// DB idempotency is checked before any upstream submission or monetary effect.
func (s *GenerationService) Create(ctx context.Context, key *APIKey, subscription *UserSubscription, account *Account, model string, body json.RawMessage, idempotency string) (*GenerationTask, error) {
	if key == nil || key.User == nil || key.Group == nil || account == nil {
		return nil, errors.New("generation identity unavailable")
	}
	if existing, err := s.Replay(ctx, key.UserID, key.ID, idempotency, body); err != nil || existing != nil {
		return existing, err
	}
	fingerprint, err := generationRequestFingerprint(body)
	if err != nil {
		return nil, err
	}
	decoded, action, err := s.plugins.DecodeVideo(ctx, account, account.NewAPIPluginVersionID(), model, body)
	if err != nil {
		return nil, err
	}
	id, err := NewGenerationTaskID()
	if err != nil {
		return nil, err
	}
	if idempotency == "" {
		idempotency = id
	}
	task := &GenerationTask{ID: id, UserID: key.UserID, APIKeyID: key.ID, GroupID: key.Group.ID, AccountID: account.ID, PluginVersionID: account.NewAPIPluginVersionID(), IdempotencyKey: idempotency, Fingerprint: fingerprint, Model: model, UpstreamModel: account.GetMappedModel(model), Platform: account.Platform, Protocol: "openai_video", Action: action, Request: decoded, ConfigEncrypted: account.GetCredential(newAPIPluginConfigEncryptedKey), State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`), Usage: json.RawMessage(`{}`)}
	task.PublicFields = generationVideoFields(body, decoded)
	task.Connection = GenerationConnection{BaseURL: account.GetCredential("base_url"), Kind: account.GetCredential("newapi_upstream_kind")}
	if task.Connection.Kind == "" {
		task.Connection.Kind = "vendor"
	}
	facts, err := s.plugins.EstimateTask(ctx, account, task)
	if err != nil {
		return nil, err
	}
	task.Usage = facts
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	resolution := "480p"
	_ = json.Unmarshal(fields["resolution"], &resolution)
	var factFields map[string]json.RawMessage
	_ = json.Unmarshal(facts, &factFields)
	_ = json.Unmarshal(factFields["resolution"], &resolution)
	var size string
	_ = json.Unmarshal(fields["size"], &size)
	if size == "1280x720" || size == "720x1280" {
		resolution = "720p"
	}
	if size == "1920x1080" || size == "1080x1920" {
		resolution = "1080p"
	}
	rule, err := generationRule(account, key.Group, model, resolution)
	if err != nil {
		return nil, err
	}
	unit := rule.Unit
	if rule.Mode == "cost_plus" {
		unit = rule.SourceUnit
	}
	quantity, ok := generationQuantity(facts, unit)
	if !ok && unit == "second" {
		quantity, ok = generationQuantity(mergeGenerationFacts(body, decoded), "second")
	}
	if !ok {
		return nil, fmt.Errorf("plugin did not provide estimated %s usage", unit)
	}
	multiplier := s.gateway.ResolveUserGroupRateMultiplier(ctx, key.UserID, key.Group.ID, key.Group.RateMultiplier)
	if key.Group.VideoRateIndependent {
		multiplier = key.Group.VideoRateMultiplier
	}
	var quote *GenerationCost
	if rule.Mode == "cost_plus" {
		if rule.SourceUnit == "upstream_cost" {
			// An explicit operator ceiling authorizes the hold. It is not an
			// upstream quote and must not be stored as one.
		} else if rule.SourceUnitPrice == nil {
			return nil, ErrGenerationPriceUnavailable
		} else {
			quote = &GenerationCost{Amount: quantity.Mul(*rule.SourceUnitPrice), Currency: rule.SourceCurrency, Unit: unit, Source: "configured_upstream_price", ObservedAt: time.Now().UTC()}
		}
	}
	task.Price, err = NewGenerationPriceSnapshot(rule, quantity, quote, decimal.NewFromFloat(multiplier))
	if err != nil {
		return nil, err
	}
	amount := task.Price.Hold.InexactFloat64()
	cmd := &UsageBillingCommand{UserID: key.UserID, APIKeyID: key.ID, AccountID: account.ID, AccountType: account.Type, APIKeyQuotaCost: amount, APIKeyRateLimitCost: amount}
	accountCost, err := rule.Price(quantity, quote, decimal.NewFromFloat(account.BillingRateMultiplier()))
	if err != nil {
		return nil, err
	}
	cmd.AccountQuotaCost = accountCost.InexactFloat64()
	cmd.AccountQuotaHasDailyLimit = account.GetQuotaDailyLimit() > 0
	cmd.AccountQuotaHasWeeklyLimit = account.GetQuotaWeeklyLimit() > 0
	if subscription != nil && key.Group.IsSubscriptionType() {
		cmd.SubscriptionID = &subscription.ID
		cmd.SubscriptionCost = amount
	} else {
		cmd.BalanceCost = amount
	}
	created, err := s.repo.CreateHeld(ctx, task, cmd)
	if err == nil {
		s.invalidate(ctx, key.UserID, key.Group.ID, key.ID)
	}
	return created, err
}

// FindIdempotent never requires a live upstream, current price or remaining
// balance. Authentication still verifies the owning user and API key.
func (s *GenerationService) FindIdempotent(ctx context.Context, user, key int64, idempotency string) (*GenerationTask, error) {
	if idempotency == "" {
		return nil, nil
	}
	task, err := s.repo.FindIdempotent(ctx, user, key, idempotency)
	if errors.Is(err, ErrGenerationTaskNotFound) {
		return nil, nil
	}
	return task, err
}

func generationRequestFingerprint(body json.RawMessage) (string, error) {
	if len(body) > 1<<20 || !json.Valid(body) {
		return "", errors.New("invalid video request snapshot")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return "", errors.New("a video request object is required")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	return HashUsageRequestPayload(canonical), nil
}

func (s *GenerationService) Replay(ctx context.Context, user, key int64, idempotency string, body json.RawMessage) (*GenerationTask, error) {
	existing, err := s.FindIdempotent(ctx, user, key, idempotency)
	if err != nil || existing == nil {
		return existing, err
	}
	fingerprint, err := generationRequestFingerprint(body)
	if err != nil {
		return nil, err
	}
	if existing.Fingerprint != fingerprint {
		return nil, ErrGenerationIdempotencyConflict
	}
	return existing, nil
}

func (s *GenerationService) Get(ctx context.Context, id string, user, key int64) (*GenerationTask, error) {
	return s.repo.GetOwned(ctx, id, user, key)
}

func (s *GenerationService) ListReview(ctx context.Context) ([]GenerationTask, error) {
	return s.repo.ListReview(ctx, 100)
}
func (s *GenerationService) Reconcile(ctx context.Context, id string, actor int64, action, upstreamID, reason string, amount *decimal.Decimal) error {
	return s.repo.Reconcile(ctx, id, actor, action, upstreamID, reason, amount)
}
func (s *GenerationService) List(ctx context.Context, user, key int64, after string, limit int, order string) ([]GenerationTask, error) {
	return s.repo.ListOwned(ctx, user, key, after, limit, order)
}
func (s *GenerationService) Render(ctx context.Context, t *GenerationTask) (map[string]any, error) {
	account, err := s.accounts.GetByID(ctx, t.AccountID)
	if err != nil {
		return nil, err
	}
	return s.plugins.RenderVideo(ctx, account, t)
}
func (s *GenerationService) Content(ctx context.Context, t *GenerationTask, method, artifact string, headers http.Header) (*http.Response, context.CancelFunc, error) {
	opCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	account, err := s.accounts.GetByID(opCtx, t.AccountID)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	slot, err := s.gateway.concurrencyService.AcquireAccountSlot(opCtx, account.ID, account.Concurrency)
	if err != nil || !slot.Acquired {
		cancel()
		return nil, nil, errors.New("video content concurrency exceeded")
	}
	response, err := s.plugins.OpenTaskContent(opCtx, account, t, method, artifact, headers)
	if err != nil {
		slot.ReleaseFunc()
		cancel()
		return nil, nil, err
	}
	return response, func() { slot.ReleaseFunc(); cancel() }, nil
}
