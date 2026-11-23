package provider

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPriceBookLongestPrefixAndExplicitZero(t *testing.T) {
	prices := map[string]float64{"web_search": 10, "web_search:model*": 8, "web_search:model-mini": 0, "custom_tool": 3}
	book, err := NewPriceBook(prices)
	require.NoError(t, err)
	prices["web_search"] = 99
	for _, tc := range []struct {
		model string
		price float64
	}{{"other", 10}, {"model-large", 8}, {"model-mini-2026", 0}, {"", 10}} {
		price, found := book.Lookup("web_search", tc.model)
		require.True(t, found)
		require.Equal(t, tc.price, price)
	}
	_, found := book.Lookup("unconfigured", "model")
	require.False(t, found)
	var none *PriceBook
	_, found = none.Lookup("web_search", "model")
	require.False(t, found)
}

func TestPriceBookRejectsAmbiguityAndInvalidSettings(t *testing.T) {
	for _, prices := range []map[string]float64{{"": 1}, {"tool:": 1}, {"tool:x*y": 1}, {"tool:x:y": 1}, {"tool\nx": 1}, {" tool": 1}, {"tool": -1}, {"tool": math.NaN()}, {"tool": math.Inf(1)}, {"tool": MaxToolPrice + 1}, {"tool:model": 1, "tool:model*": 2}, {strings.Repeat("x", 129): 1}} {
		_, err := NewPriceBook(prices)
		require.Error(t, err)
	}
	prices := make(map[string]float64)
	for i := 0; i <= MaxToolPrices; i++ {
		prices[strings.Repeat("a", i/100)+string(rune(0x400+i))] = 1
	}
	_, err := NewPriceBook(prices)
	require.Error(t, err)
}

func TestToolChargesRequirePricesPreserveZeroAndAreStable(t *testing.T) {
	var usage ToolUsage
	require.NoError(t, usage.Add(map[string]int{"web_search": 2, "custom_tool": 3, "not_used": 0}))
	book, err := NewPriceBook(map[string]float64{"web_search": 10, "custom_tool": 2.5})
	require.NoError(t, err)
	lookup := func(name string) (float64, bool) { return book.Lookup(name, "model") }
	charges, err := usage.Charges(lookup)
	require.NoError(t, err)
	require.InDelta(t, 0.0275, charges.TotalUSD, 1e-12)
	require.Equal(t, []string{"custom_tool", "not_used", "web_search"}, []string{charges.Items[0].Name, charges.Items[1].Name, charges.Items[2].Name})
	require.Nil(t, charges.Items[1].PricePer1K)
	_, err = usage.Charges(nil)
	require.Error(t, err)
	charges.Items[0].Count = 999
	*charges.Items[0].PricePer1K = 999
	charges, err = usage.Charges(lookup)
	require.NoError(t, err)
	require.Equal(t, 3, charges.Items[0].Count)
	require.Equal(t, 2.5, *charges.Items[0].PricePer1K)
}

func TestToolBillingSnapshotCopiesPricesAndPreservesFreeUsage(t *testing.T) {
	var usage ToolUsage
	require.NoError(t, usage.Add(map[string]int{"file_search": 3, "free": 2, "unused": 0}))
	charges, err := usage.Charges(func(name string) (float64, bool) {
		return map[string]float64{"file_search": 2.5, "free": 0}[name], name != "unused"
	})
	require.NoError(t, err)
	snapshot, err := charges.Snapshot(2)
	require.NoError(t, err)
	require.Equal(t, 1, snapshot.SchemaVersion)
	require.InDelta(t, 0.0075, snapshot.TotalUSD, 1e-12)
	require.InDelta(t, 0.015, snapshot.ActualUSD, 1e-12)
	require.Equal(t, 2.0, snapshot.RateMultiplier)
	require.NotNil(t, snapshot.Items[1].PricePer1K)
	require.Zero(t, *snapshot.Items[1].PricePer1K)
	require.Nil(t, snapshot.Items[2].PricePer1K)
	*charges.Items[0].PricePer1K = 999
	charges.Items[0].Count = 999
	require.Equal(t, 2.5, *snapshot.Items[0].PricePer1K)
	require.Equal(t, 3, snapshot.Items[0].Count)
	free, err := snapshot.ToolCharges.Snapshot(0)
	require.NoError(t, err)
	require.Zero(t, free.ActualUSD)
	require.Positive(t, free.TotalUSD)
}

func TestToolBillingSnapshotRejectsInconsistentCharges(t *testing.T) {
	for _, multiplier := range []float64{-1, math.NaN(), math.Inf(1)} {
		_, err := (&ToolCharges{}).Snapshot(multiplier)
		require.Error(t, err)
	}
	for _, charges := range []ToolCharges{
		{Items: []ToolCharge{{Name: "tool", Count: 1}}},
		{Items: []ToolCharge{{Name: "tool", Count: -1}}},
		{Items: []ToolCharge{{Name: "tool", Count: 0}, {Name: "tool", Count: 0}}},
		{TotalUSD: 1},
		{TotalUSD: math.NaN()},
		{Items: []ToolCharge{{Name: "tool", Count: 0, CostUSD: 1}}},
	} {
		_, err := charges.Snapshot(1)
		require.Error(t, err)
	}
	none, err := (*ToolCharges)(nil).Snapshot(1)
	require.NoError(t, err)
	require.Nil(t, none)
}
