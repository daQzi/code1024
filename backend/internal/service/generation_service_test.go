package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type generationReplayFixture struct {
	GenerationTaskRepository
	task    *GenerationTask
	err     error
	lookups int
}

func (r *generationReplayFixture) FindIdempotent(_ context.Context, user, key int64, idempotency string) (*GenerationTask, error) {
	r.lookups++
	if r.err != nil {
		return nil, r.err
	}
	if r.task == nil || r.task.UserID != user || r.task.APIKeyID != key || r.task.IdempotencyKey != idempotency {
		return nil, ErrGenerationTaskNotFound
	}
	return r.task, nil
}

func TestGenerationReplayBeforeUpstreamAndPricing(t *testing.T) {
	fingerprint, err := generationRequestFingerprint(json.RawMessage(`{"model":"sora-2","prompt":"Ocean","metadata":{"b":2,"a":1}}`))
	require.NoError(t, err)
	task := &GenerationTask{ID: "video_original", UserID: 1, APIKeyID: 2, IdempotencyKey: "accepted", Fingerprint: fingerprint}
	repo := &generationReplayFixture{task: task}
	s := &GenerationService{repo: repo} // No live plugin, account connection or billing services.
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, Group: &Group{ID: 3}}
	replayed, err := s.Create(context.Background(), key, nil, &Account{}, "sora-2", json.RawMessage("{\n \"metadata\":{\"a\":1,\"b\":2},\"prompt\":\"Ocean\",\"model\":\"sora-2\"}"), "accepted")
	require.NoError(t, err)
	require.Same(t, task, replayed)
	_, err = s.Replay(context.Background(), 1, 2, "accepted", json.RawMessage(`{"model":"sora-2","prompt":"Different"}`))
	require.ErrorIs(t, err, ErrGenerationIdempotencyConflict)
	for _, identity := range [][2]int64{{9, 2}, {1, 9}} {
		replayed, err = s.FindIdempotent(context.Background(), identity[0], identity[1], "accepted")
		require.NoError(t, err)
		require.Nil(t, replayed)
	}
	repo.err = errors.New("database unavailable")
	_, err = s.FindIdempotent(context.Background(), 1, 2, "accepted")
	require.ErrorIs(t, err, repo.err)
	repo.lookups = 0
	_, err = s.FindIdempotent(context.Background(), 1, 2, "")
	require.NoError(t, err)
	require.Zero(t, repo.lookups)
}

type generationSettlementFixture struct {
	GenerationTaskRepository
	saved, settled bool
	amount         decimal.Decimal
}

func (r *generationSettlementFixture) SaveProgress(context.Context, *GenerationTask, time.Duration) error {
	r.saved = true
	return nil
}
func (r *generationSettlementFixture) Settle(_ context.Context, task *GenerationTask, actual decimal.Decimal, _ *GenerationCost) error {
	r.settled, r.amount = true, actual
	task.BillingStatus = GenerationBillingSettled
	return nil
}

func TestGenerationSettlementRequiresFinalUnits(t *testing.T) {
	unit := decimal.RequireFromString("0.1")
	price, err := NewGenerationPriceSnapshot(GenerationPriceRule{Mode: "fixed", Unit: "second", UnitPrice: &unit}, decimal.NewFromInt(8), nil, decimal.NewFromInt(1))
	require.NoError(t, err)
	task := &GenerationTask{Status: GenerationSucceeded, BillingStatus: GenerationBillingPending, Price: price, Usage: json.RawMessage(`{"seconds":8}`)}
	repo := &generationSettlementFixture{}
	s := &GenerationService{repo: repo}
	require.NoError(t, s.settle(context.Background(), task))
	require.True(t, repo.saved)
	require.False(t, repo.settled, "an admission estimate is not a final observation")
	require.Equal(t, GenerationBillingReview, task.BillingStatus)

	task.BillingStatus = GenerationBillingPending
	task.Usage = json.RawMessage(`{"seconds":4,"_host_final_usage":{"seconds":4}}`)
	require.NoError(t, s.settle(context.Background(), task))
	require.True(t, repo.settled)
	require.Equal(t, "0.4", repo.amount.String())
}

func TestGenerationSettlementFailureRefundsWithoutUsage(t *testing.T) {
	unit := decimal.NewFromInt(1)
	price, err := NewGenerationPriceSnapshot(GenerationPriceRule{Mode: "fixed", Unit: "token", UnitPrice: &unit}, decimal.NewFromInt(8), nil, decimal.NewFromInt(1))
	require.NoError(t, err)
	task := &GenerationTask{Status: GenerationFailed, BillingStatus: GenerationBillingPending, Price: price, Usage: json.RawMessage(`{}`)}
	repo := &generationSettlementFixture{}
	require.NoError(t, (&GenerationService{repo: repo}).settle(context.Background(), task))
	require.True(t, repo.settled)
	require.True(t, repo.amount.IsZero())
}

func TestGenerationSettlementNeverChargesAboveHold(t *testing.T) {
	unit := decimal.NewFromInt(1)
	price, err := NewGenerationPriceSnapshot(GenerationPriceRule{Mode: "fixed", Unit: "token", UnitPrice: &unit}, decimal.NewFromInt(8), nil, decimal.NewFromInt(1))
	require.NoError(t, err)
	task := &GenerationTask{Status: GenerationSucceeded, BillingStatus: GenerationBillingPending, Price: price, Usage: json.RawMessage(`{"tokens":12,"_host_final_usage":{"tokens":12}}`)}
	repo := &generationSettlementFixture{}
	require.NoError(t, (&GenerationService{repo: repo}).settle(context.Background(), task))
	require.True(t, repo.saved)
	require.False(t, repo.settled)
	require.Equal(t, GenerationBillingReview, task.BillingStatus)
}

func TestGenerationVideoPublicFieldsSurvivePluginRewrite(t *testing.T) {
	fields := generationVideoFields(json.RawMessage(`{"seconds":"8","size":"1280x720","prompt":"Ocean","api_key":"private"}`), json.RawMessage(`{"duration":8,"resolution":"720p","token":"secret"}`))
	task := &GenerationTask{ID: "video_fixture", Model: "public-alias", PublicFields: fields, Status: GenerationSubmitting}
	view := task.VideoView()
	require.Equal(t, "8", view["seconds"])
	require.Equal(t, "1280x720", view["size"])
	require.Equal(t, "Ocean", view["prompt"])
	require.Equal(t, "in_progress", view["status"])
	require.NotContains(t, view, "api_key")
	require.NotContains(t, view, "token")
	require.Nil(t, view["completed_at"])
	fallback := generationVideoFields(json.RawMessage(`{}`), json.RawMessage(`{"duration":12}`))
	require.Equal(t, "12", fallback.Seconds)
}
