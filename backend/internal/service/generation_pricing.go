package service

import (
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/shopspring/decimal"
)

// Compatibility aliases keep the existing ledger and HTTP contracts stable.
// Pricing rules and immutable snapshots belong to the standalone engine.
type GenerationPriceRule = generation.PriceRule
type GenerationCost = generation.Cost
type GenerationQuoteComparison = generation.QuoteComparison
type GenerationPriceSnapshot = generation.PriceSnapshot

var ErrGenerationPriceUnavailable = generation.ErrPriceUnavailable
var ErrGenerationCostPending = generation.ErrCostPending
var ErrGenerationPriceExceedsHold = generation.ErrPriceExceedsHold
var ErrGenerationQuoteExpired = generation.ErrQuoteExpired

func NewGenerationPriceSnapshot(rule GenerationPriceRule, quantity decimal.Decimal, quote *GenerationCost, multiplier decimal.Decimal) (GenerationPriceSnapshot, error) {
	return generation.NewPriceSnapshot(rule, quantity, quote, multiplier)
}
