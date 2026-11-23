package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/shopspring/decimal"
)

const NewAPIVideoPricingKey = "newapi_video_pricing"

type GenerationSubmissionError = generation.SubmissionError

// GenerationService owns a bounded durable executor. No request or gin context
// crosses this boundary. A lost submitting lease requires reconciliation.
type GenerationService struct {
	repo          GenerationTaskRepository
	plugins       *NewAPIPluginService
	accounts      AccountRepository
	gateway       *OpenAIGatewayService
	cache         *BillingCacheService
	keys          *APIKeyService
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	once          sync.Once
	engine        *generation.Executor
	workers       *generation.Workers
	lifecycleMu   sync.Mutex
	started       bool
	stopped       bool
	nativeAliases nativeAliasCatalog
}

func NewGenerationService(repo GenerationTaskRepository, plugins *NewAPIPluginService, accounts AccountRepository, gateway *OpenAIGatewayService, cache *BillingCacheService, keys *APIKeyService) *GenerationService {
	s := NewGenerationHostService(repo, plugins, accounts, gateway, cache, keys)
	_ = s.StartTaskExecutor(generation.NewExecutor(s.TaskExecutorPorts()))
	return s
}

// NewGenerationHostService prepares the existing admission/ledger facade
// without starting a second worker. Production composition is in newapihost.
func NewGenerationHostService(repo GenerationTaskRepository, plugins *NewAPIPluginService, accounts AccountRepository, gateway *OpenAIGatewayService, cache *BillingCacheService, keys *APIKeyService) *GenerationService {
	return &GenerationService{repo: repo, plugins: plugins, accounts: accounts, gateway: gateway, cache: cache, keys: keys}
}

// StartTaskExecutor is one-shot for this facade. Duplicate assembly must never
// create two active pools for the same host instance.
func (s *GenerationService) StartTaskExecutor(executor *generation.Executor) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started || s.stopped || executor == nil {
		return errors.New("generation executor already started, stopped or unavailable")
	}
	s.started = true
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.engine = executor
	s.workers = s.engine.Start(ctx, 2)
	s.startHostMaintenance(ctx)
	return nil
}
func (s *GenerationService) Stop() {
	if s != nil {
		s.once.Do(func() {
			s.lifecycleMu.Lock()
			s.stopped = true
			cancel, workers := s.cancel, s.workers
			s.lifecycleMu.Unlock()
			if cancel != nil {
				cancel()
			}
			workers.Stop()
			s.wg.Wait()
		})
	}
}

func (s *GenerationService) process(ctx context.Context, task *GenerationTask) error {
	return s.taskExecutor().Process(ctx, task)
}

func (s *GenerationService) settle(ctx context.Context, task *GenerationTask) error {
	return s.taskExecutor().Settle(ctx, task)
}

func (s *GenerationService) afterTaskSettlement(ctx context.Context, task *GenerationTask, actual decimal.Decimal) {
	// Production repositories commit the platform quota projection and its
	// delivery event with settlement. Only legacy/test repositories retain the
	// former path; a failed delivery must never increment this quota twice.
	_, durableQuota := s.repo.(GenerationQuotaOutboxRepository)
	if !durableQuota && task.BillingStatus == GenerationBillingSettled && task.SubscriptionID == nil && actual.IsPositive() && s.cache != nil && s.gateway != nil && s.gateway.userPlatformQuotaRepo != nil && s.cache.HasUserPlatformQuotaLimit(ctx, task.UserID, task.Platform) {
		s.cache.IncrementUserPlatformQuotaUsage(task.UserID, task.Platform, actual.InexactFloat64())
		if s.gateway.cfg == nil || !s.gateway.cfg.Database.UserPlatformQuotaFlusherEnabled {
			persistPostUsagePlatformQuota(ctx, s.gateway.userPlatformQuotaRepo, task.UserID, task.Platform, actual.InexactFloat64())
		}
	}
	s.invalidate(ctx, task.UserID, task.GroupID, task.APIKeyID)
	if s.plugins != nil && task.BillingStatus == GenerationBillingSettled {
		s.plugins.enqueueTaskReport(task, actual)
	}
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
	return generation.Quantity(facts, unit)
}
func generationBillingPending(task *GenerationTask) bool         { return generation.BillingPendingFor(task) }
func generationObservedCost(raw json.RawMessage) *GenerationCost { return generation.ObservedCost(raw) }

func generationRule(account *Account, group *Group, model, resolution string) (GenerationPriceRule, error) {
	return generationRuleWithFallback(account, group, model, resolution, true)
}

func generationRuleWithFallback(account *Account, group *Group, model, resolution string, video bool) (GenerationPriceRule, error) {
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
	if video && group != nil {
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
	return s.CreateVideo(ctx, key, subscription, account, model, body, idempotency, GenerationProtocolOpenAI)
}

// CreateVideo accepts a wire protocol, but all accepted requests share the same
// durable executor and financial ledger. Native upstream IDs remain private.
func (s *GenerationService) CreateVideo(ctx context.Context, key *APIKey, subscription *UserSubscription, account *Account, model string, body json.RawMessage, idempotency, protocol string) (*GenerationTask, error) {
	if _, ok := generationVideoIngress(protocol); !ok {
		return nil, errors.New("unsupported video protocol")
	}
	if protocol == GenerationProtocolSunoTask && GenerationTaskSubmitPlugin(ctx) != "sunoapi" {
		return nil, ErrGenerationDecode
	}
	ctx = withGenerationProtocol(ctx, protocol)
	if key == nil || key.User == nil || key.Group == nil || account == nil {
		return nil, errors.New("generation identity unavailable")
	}
	if existing, err := s.Replay(ctx, key.UserID, key.ID, idempotency, body); err != nil || existing != nil {
		if existing != nil && (!IsVideoGenerationProtocol(protocol) || GenerationTaskSubmitPlugin(ctx) != "") && existing.GroupID != key.Group.ID {
			return nil, ErrGenerationIdempotencyConflict
		}
		return existing, err
	}
	fingerprint, err := generationClientFingerprint(ctx, body)
	if err != nil {
		return nil, err
	}
	if err = validateGenerationVideoPlan(ctx, account, model, protocol); err != nil {
		return nil, err
	}
	if err = s.validateTaskSubmitBinding(ctx, account, model, protocol, body); err != nil {
		return nil, err
	}
	clientBody := body
	ctx, releaseRequestMemory := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer releaseRequestMemory()
	if pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext); pinned.plan.hasLateIngressFor(account.ID, 61) {
		ctx, err = s.PrepareVideoPolicyFiles(ctx, key, body, protocol)
		if err != nil {
			return nil, err
		}
	}
	body, err = s.plugins.taskRequestPluginLateIngress(ctx, key, account, model, body)
	if err != nil {
		return nil, err
	}
	if GenerationTaskSubmitPlugin(ctx) != "" {
		if current, err := GenerationTaskSubmitModel(body); err != nil || current != model {
			return nil, ErrGenerationDecode
		}
	}
	if protocol == GenerationProtocolSunoTask && !sunoTaskAuditTextUnchanged(clientBody, body) {
		return nil, ErrGenerationDecode
	}
	body, files, err := s.prepareVideoFiles(ctx, key, body, protocol)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, generationDecodeFilesKey{}, files)
	decoded, action, err := s.plugins.DecodeVideoProtocol(ctx, account, account.NewAPIPluginVersionID(), model, body, protocol)
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
	task := &GenerationTask{ID: id, UserID: key.UserID, APIKeyID: key.ID, GroupID: key.Group.ID, AccountID: account.ID, PluginVersionID: account.NewAPIPluginVersionID(), IdempotencyKey: idempotency, Fingerprint: fingerprint, Model: model, UpstreamModel: account.GetMappedModel(model), Platform: account.Platform, Protocol: protocol, Action: action, Request: decoded, ConfigEncrypted: account.GetCredential(newAPIPluginConfigEncryptedKey), State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`), Usage: json.RawMessage(`{}`)}
	if IsVideoGenerationProtocol(protocol) {
		task.PublicFields = generationNativePublicFields(body, decoded, protocol)
	} else if protocol == GenerationProtocolOpenAIResponses {
		task.PublicFields.ResponsesBackground = generationResponsesModeFromContext(ctx) == "background"
	}
	remix, isRemix := ctx.Value(generationRemixKey{}).(generationRemix)
	if isRemix {
		task.PublicFields.Seconds, task.PublicFields.Size = remix.seconds, remix.size
	}
	task.Files = files
	if err = s.plugins.initializeTaskPreparation(ctx, task); err != nil {
		return nil, err
	}
	if plan, _ := ctx.Value(generationVideoPlanKey{}).(*generationVideoPlan); plan != nil {
		task.Origins = plan.origins
	}
	task.RequestPluginsEncrypted, err = s.plugins.snapshotTaskRequestPlugins(ctx, key, model)
	if err != nil {
		return nil, err
	}
	form, _ := ctx.Value(generationMultipartBodyKey{}).(bool)
	task.RequestReportEncrypted = s.plugins.snapshotTaskReport(ctx, key, model, clientBody, form)
	task.Connection = GenerationConnection{BaseURL: account.GetCredential("base_url"), Kind: account.GetCredential("newapi_upstream_kind")}
	if task.Connection.Kind == "" {
		task.Connection.Kind = "vendor"
	}
	account, err = s.plugins.prepareTaskCredential(ctx, account, task, true)
	if err != nil {
		return nil, err
	}
	facts, err := s.plugins.EstimateTask(ctx, account, task)
	if err != nil {
		return nil, err
	}
	if isRemix {
		facts = mergeGenerationFacts(generationRemixEstimatedFacts(remix), facts)
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
	if isRemix {
		size = remix.size
	}
	if size == "1280x720" || size == "720x1280" {
		resolution = "720p"
	}
	if size == "1920x1080" || size == "1080x1920" {
		resolution = "1080p"
	}
	rule, err := generationRuleWithFallback(account, key.Group, model, resolution, IsVideoGenerationProtocol(protocol))
	if err != nil {
		return nil, err
	}
	unit := rule.Unit
	if rule.Mode == "cost_plus" {
		unit = rule.SourceUnit
	}
	quantity, ok := generationQuantity(facts, unit)
	if rule.Mode == "expression" {
		quantity, ok = decimal.NewFromInt(1), true
	}
	if !ok && unit == "second" {
		quantity, ok = generationQuantity(mergeGenerationFacts(body, decoded), "second")
	}
	multiplier := s.gateway.ResolveUserGroupRateMultiplier(ctx, key.UserID, key.Group.ID, key.Group.RateMultiplier)
	if IsVideoGenerationProtocol(protocol) && key.Group.VideoRateIndependent {
		multiplier = key.Group.VideoRateMultiplier
	}
	var quote *GenerationCost
	if rule.Mode == "cost_plus" {
		pluginQuote, quoteErr := s.plugins.QuoteTask(ctx, account, task)
		if quoteErr != nil {
			return nil, quoteErr
		}
		if pluginQuote != nil {
			quote = pluginQuote
			if !ok {
				quantity, ok = decimal.NewFromInt(1), true
			}
		} else if rule.UsesFinalBill() {
			// An explicit operator ceiling authorizes the hold. It is not an
			// upstream quote and must not be stored as one.
		} else if rule.SourceUnitPrice == nil {
			return nil, ErrGenerationPriceUnavailable
		} else {
			if !ok {
				return nil, fmt.Errorf("plugin did not provide estimated %s usage", unit)
			}
			quote = &GenerationCost{Amount: quantity.Mul(*rule.SourceUnitPrice), Currency: rule.SourceCurrency, Unit: unit, Source: "configured_upstream_price", ObservedAt: time.Now().UTC()}
		}
	}
	if !ok {
		return nil, fmt.Errorf("plugin did not provide estimated %s usage", unit)
	}
	if rule.Mode == "expression" {
		version, loadErr := s.plugins.repo.Get(ctx, task.PluginVersionID)
		if loadErr != nil {
			return nil, loadErr
		}
		schema, _ := version.Meta.UsageForModels(task.UpstreamModel, task.Model)
		task.Price, err = NewGenerationExpressionSnapshotWithRequest(rule, schema, facts, body, generationRequestHeaders(ctx), decimal.NewFromFloat(multiplier))
	} else {
		task.Price, err = NewGenerationPriceSnapshot(rule, quantity, quote, decimal.NewFromFloat(multiplier))
	}
	if err != nil {
		return nil, err
	}
	amount := task.Price.Hold.InexactFloat64()
	cmd := &UsageBillingCommand{UserID: key.UserID, APIKeyID: key.ID, AccountID: account.ID, AccountType: account.Type, APIKeyQuotaCost: amount, APIKeyRateLimitCost: amount}
	accountRate := decimal.NewFromFloat(account.BillingRateMultiplier())
	var accountCost decimal.Decimal
	if rule.Mode == "expression" || rule.UsesFinalBill() {
		accountCost = rule.Maximum.Mul(accountRate).Round(UsageBillingMonetaryScale)
	} else {
		accountCost, err = rule.Price(quantity, quote, accountRate)
	}
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

type generationClientFingerprintKey struct{}

// WithGenerationClientRequest keeps idempotency tied to the original client
// JSON, while the task stores and bills the transformed request. Only a hash
// is retained; policy changes must not change the identity of an accepted job.
func WithGenerationClientRequest(ctx context.Context, body json.RawMessage) (context.Context, error) {
	if _, ok := ctx.Value(generationClientFingerprintKey{}).(string); ok {
		return ctx, nil // A streamed multipart ingress already fixed the original identity.
	}
	fingerprint, err := generationRequestFingerprint(body)
	if err != nil {
		return nil, err
	}
	return context.WithValue(ctx, generationClientFingerprintKey{}, fingerprint), nil
}

func generationClientFingerprint(ctx context.Context, body json.RawMessage) (string, error) {
	if fingerprint, ok := ctx.Value(generationClientFingerprintKey{}).(string); ok {
		return generationProtocolFingerprint(ctx, fingerprint), nil
	}
	fingerprint, err := generationRequestFingerprint(body)
	return generationProtocolFingerprint(ctx, fingerprint), err
}

func (s *GenerationService) Replay(ctx context.Context, user, key int64, idempotency string, body json.RawMessage) (*GenerationTask, error) {
	existing, err := s.FindIdempotent(ctx, user, key, idempotency)
	if err != nil || existing == nil {
		return existing, err
	}
	fingerprint, err := generationClientFingerprint(ctx, body)
	if err != nil {
		return nil, err
	}
	if existing.Fingerprint != fingerprint {
		// Before native ingress support, /videos/generations was a canonical
		// alias. Preserve reservations already accepted through that alias.
		protocol, _ := ctx.Value(generationProtocolKey{}).(string)
		if protocol == GenerationProtocolXAI && (existing.Protocol == "" || existing.Protocol == GenerationProtocolOpenAI) {
			legacy, legacyErr := generationClientFingerprint(withGenerationProtocol(ctx, GenerationProtocolOpenAI), body)
			if legacyErr == nil && existing.Fingerprint == legacy {
				return existing, nil
			}
		}
		if plugin := GenerationTaskSubmitPlugin(ctx); plugin != "" && plugin != "sunoapi" && protocol == GenerationProtocolOpenAI && existing.Protocol == GenerationProtocolGenericTask {
			generic, genericErr := generationClientFingerprint(withGenerationProtocol(ctx, GenerationProtocolGenericTask), body)
			if genericErr == nil && existing.Fingerprint == generic {
				return existing, nil
			}
		}
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
	return s.generationContent(ctx, t, method, artifact, headers, false)
}

func (s *GenerationService) generationContent(ctx context.Context, t *GenerationTask, method, artifact string, headers http.Header, declaredOnly bool) (*http.Response, context.CancelFunc, error) {
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
	response, err := s.plugins.openTaskContent(opCtx, account, t, method, artifact, headers, declaredOnly)
	if err != nil {
		slot.ReleaseFunc()
		cancel()
		return nil, nil, err
	}
	return response, func() { slot.ReleaseFunc(); cancel() }, nil
}
