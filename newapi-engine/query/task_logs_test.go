package query_test

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/query"
	"github.com/Wei-Shaw/sub2api/newapi-engine/taskprice"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type queryFixture struct {
	item   query.Detail
	filter query.Filter
	owner  int64
}

func (r *queryFixture) ListTaskLogs(_ context.Context, filter query.Filter) ([]query.Log, int64, error) {
	r.filter = filter
	return []query.Log{r.item.TaskLog}, 1, nil
}
func (r *queryFixture) GetTaskLog(_ context.Context, _ string, owner int64) (*query.Detail, error) {
	r.owner = owner
	return &r.item, nil
}

func TestConsoleOwnershipAndRedactionAreIndependentOfExecution(t *testing.T) {
	mult := decimal.RequireFromString("1.5")
	repo := &queryFixture{item: query.Detail{
		TaskLog:       query.Log{ID: "video_fixture", UserID: 7, AccountID: 9, APIKeyID: 8, UpstreamID: "private-upstream"},
		NativeActions: []string{"poll"},
		Billing: &query.BillingSummary{
			Mode:            "expression",
			GroupMultiplier: &mult,
			MatchedTier:     "hd",
			RequestRules:    []taskprice.RequestRuleTrace{{Cond: `param("quality") == "pro"`, Multiplier: 1.5, Matched: true}},
			QuoteComparison: &generation.QuoteComparison{QuotedCost: decimal.NewFromInt(2), QuotedCurrency: "USD", QuotedUnit: "upstream_cost", QuotedSource: "quote_api"},
		},
	}}
	service := query.NewService(repo)
	items, _, err := service.List(context.Background(), query.Access{UserID: 7}, query.Filter{UserID: 99, AccountID: 9})
	require.NoError(t, err)
	require.EqualValues(t, 7, repo.filter.UserID, "filter input cannot override authenticated ownership")
	require.Zero(t, repo.filter.AccountID)
	require.Zero(t, items[0].UserID)
	require.Empty(t, items[0].UpstreamID)
	detail, err := service.Get(context.Background(), query.Access{UserID: 7}, "video_fixture")
	require.NoError(t, err)
	require.Nil(t, detail.NativeActions)
	require.Nil(t, detail.Billing, "ordinary users must never see upstream cost, markup, or quote comparison details")
	require.Zero(t, detail.AccountID)
	require.EqualValues(t, 7, repo.owner)
	require.Equal(t, "private-upstream", repo.item.UpstreamID, "public redaction cannot mutate an administrator's stored projection")
	admin, err := service.Get(context.Background(), query.Access{Administrator: true}, "video_fixture")
	require.NoError(t, err)
	require.Equal(t, "private-upstream", admin.UpstreamID)
	require.Equal(t, []string{"poll"}, admin.NativeActions)
	require.NotNil(t, admin.Billing)
	require.Equal(t, "expression", admin.Billing.Mode)
	require.Equal(t, "hd", admin.Billing.MatchedTier)
	require.Len(t, admin.Billing.RequestRules, 1)
	require.NotNil(t, admin.Billing.QuoteComparison)
	require.Equal(t, "2", admin.Billing.QuoteComparison.QuotedCost.String())
	_, err = service.Get(context.Background(), query.Access{UserID: 99}, "video_fixture")
	require.ErrorIs(t, err, generation.ErrTaskNotFound)
	_, _, err = service.List(context.Background(), query.Access{}, query.Filter{UserID: 7})
	require.ErrorIs(t, err, generation.ErrTaskNotFound)
}
