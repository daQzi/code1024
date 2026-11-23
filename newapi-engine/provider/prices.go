// SPDX-License-Identifier: AGPL-3.0-or-later
// Model-prefix lookup follows QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f,
// setting/operation_setting/tools.go. Prices are supplied by the host;
// vendor-specific fallback prices and currencies are deliberately not seeded.
package provider

import (
	"errors"
	"math"
	"slices"
	"strings"
	"unicode"
)

const MaxToolPrices = 512
const MaxToolPrice = 1e12

type pricePrefix struct {
	prefix string
	price  float64
}

// PriceBook is immutable after construction and may be shared between requests.
// A missing price differs from an explicitly configured free price.
type PriceBook struct {
	defaults map[string]float64
	prefixes map[string][]pricePrefix
}

func NewPriceBook(prices map[string]float64) (*PriceBook, error) {
	if len(prices) > MaxToolPrices {
		return nil, errors.New("provider tool prices exceed 512 entries")
	}
	book := &PriceBook{defaults: make(map[string]float64), prefixes: make(map[string][]pricePrefix)}
	for key, price := range prices {
		name, model, qualified := strings.Cut(key, ":")
		if len(key) > 384 || strings.TrimSpace(key) != key || strings.TrimSpace(name) == "" || len(name) > 128 ||
			strings.IndexFunc(key, unicode.IsControl) >= 0 || strings.Contains(name, "*") ||
			qualified && (model == "" || strings.ContainsAny(strings.TrimSuffix(model, "*"), ":*")) ||
			math.IsNaN(price) || math.IsInf(price, 0) || price < 0 || price > MaxToolPrice {
			return nil, errors.New("invalid provider tool price key or amount")
		}
		if !qualified {
			book.defaults[name] = price
			continue
		}
		prefix := strings.TrimSuffix(model, "*")
		for _, existing := range book.prefixes[name] {
			if existing.prefix == prefix {
				return nil, errors.New("ambiguous provider tool price model prefix")
			}
		}
		book.prefixes[name] = append(book.prefixes[name], pricePrefix{prefix: prefix, price: price})
	}
	for name := range book.prefixes {
		slices.SortFunc(book.prefixes[name], func(a, b pricePrefix) int {
			if len(a.prefix) != len(b.prefix) {
				return len(b.prefix) - len(a.prefix)
			}
			return strings.Compare(a.prefix, b.prefix)
		})
	}
	return book, nil
}

// Lookup uses the baseline's longest prefix first, then the tool default.
// A suffix * is optional; without it the model selector is still a prefix.
func (b *PriceBook) Lookup(name, model string) (float64, bool) {
	if b == nil {
		return 0, false
	}
	if model != "" {
		for _, entry := range b.prefixes[name] {
			if strings.HasPrefix(model, entry.prefix) {
				return entry.price, true
			}
		}
	}
	price, found := b.defaults[name]
	return price, found
}

type ToolCharge struct {
	Name       string   `json:"name"`
	Count      int      `json:"count"`
	PricePer1K *float64 `json:"price_per_1k,omitempty"`
	CostUSD    float64  `json:"cost_usd"`
}

// ToolCharges is an immutable request projection passed to the host's single
// ledger. It does not debit a wallet or apply a host/group multiplier.
type ToolCharges struct {
	Items    []ToolCharge `json:"items"`
	TotalUSD float64      `json:"total_usd"`
}

// ToolBilling freezes the public selling prices and the host's tool-specific
// multiplier. It is a billing trace, not a separate ledger or upstream cost.
type ToolBilling struct {
	SchemaVersion  int     `json:"schema_version"`
	RateMultiplier float64 `json:"rate_multiplier"`
	ActualUSD      float64 `json:"actual_usd"`
	ToolCharges
}

func (c *ToolCharges) Snapshot(multiplier float64) (*ToolBilling, error) {
	if c == nil {
		return nil, nil
	}
	if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier < 0 || len(c.Items) > MaxToolNames {
		return nil, errors.New("invalid provider tool billing multiplier or items")
	}
	counts := ToolUsage{}
	prices := make(map[string]float64, len(c.Items))
	for _, item := range c.Items {
		if _, duplicate := counts.counts[item.Name]; duplicate {
			return nil, errors.New("duplicate provider tool billing item")
		}
		if err := counts.Add(map[string]int{item.Name: item.Count}); err != nil {
			return nil, err
		}
		if item.PricePer1K != nil {
			prices[item.Name] = *item.PricePer1K
		}
	}
	// Rebuild and deep-copy only known billing fields. Reject an inconsistent
	// trace before the host debits funds, rather than recording another total.
	cloned, err := counts.Charges(func(name string) (float64, bool) { price, ok := prices[name]; return price, ok })
	if err != nil || cloned.TotalUSD != c.TotalUSD {
		return nil, errors.New("inconsistent provider tool billing total")
	}
	byName := make(map[string]float64, len(c.Items))
	for _, item := range c.Items {
		byName[item.Name] = item.CostUSD
	}
	for _, item := range cloned.Items {
		if item.CostUSD != byName[item.Name] {
			return nil, errors.New("inconsistent provider tool billing item")
		}
	}
	actual := cloned.TotalUSD * multiplier
	if math.IsNaN(actual) || math.IsInf(actual, 0) {
		return nil, errors.New("provider tool billing amount exceeds limit")
	}
	return &ToolBilling{SchemaVersion: 1, RateMultiplier: multiplier, ActualUSD: actual, ToolCharges: cloned}, nil
}

// Charges requires a known price for every positive count. Zero usage remains
// explicit even when no price is configured; it never invents a vendor price.
func (u *ToolUsage) Charges(lookup func(string) (float64, bool)) (ToolCharges, error) {
	counts := u.Counts()
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	slices.Sort(names)
	charges := ToolCharges{Items: make([]ToolCharge, 0, len(names))}
	for _, name := range names {
		price, found := 0.0, false
		if lookup != nil {
			price, found = lookup(name)
		}
		count := counts[name]
		if count > 0 && !found || found && (math.IsNaN(price) || math.IsInf(price, 0) || price < 0 || price > MaxToolPrice) {
			return ToolCharges{}, errors.New("provider tool requires an explicit finite non-negative price")
		}
		item := ToolCharge{Name: name, Count: count}
		if found {
			item.PricePer1K = new(price)
			item.CostUSD = float64(count) / 1000 * price
		}
		charges.Items = append(charges.Items, item)
		charges.TotalUSD += item.CostUSD
	}
	return charges, nil
}
