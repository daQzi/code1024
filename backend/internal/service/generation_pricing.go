package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// GenerationPriceRule is an operator-controlled rule. All amounts settle in
// the site's USD ledger. Currency/unit conversion is explicit and immutable.
type GenerationPriceRule struct {
	Mode            string           `json:"mode"` // fixed / cost_plus
	Unit            string           `json:"unit"` // request / second / token / credit
	UnitPrice       *decimal.Decimal `json:"unit_price,omitempty"`
	SourceCurrency  string           `json:"source_currency,omitempty"`
	SourceUnit      string           `json:"source_unit,omitempty"`
	SourceUnitPrice *decimal.Decimal `json:"source_unit_price,omitempty"`
	ExchangeRate    *decimal.Decimal `json:"exchange_rate,omitempty"`
	Markup          *decimal.Decimal `json:"markup,omitempty"`
	ServiceFee      decimal.Decimal  `json:"service_fee"`
	Minimum         decimal.Decimal  `json:"minimum"`
	Maximum         *decimal.Decimal `json:"maximum,omitempty"`
}

type GenerationCost struct {
	Amount     decimal.Decimal `json:"amount"`
	Currency   string          `json:"currency"`
	Unit       string          `json:"unit"`
	Final      bool            `json:"final"`
	Source     string          `json:"source"`
	ObservedAt time.Time       `json:"observed_at"`
}

type GenerationPriceSnapshot struct {
	Rule            GenerationPriceRule `json:"rule"`
	GroupMultiplier decimal.Decimal     `json:"group_multiplier"`
	Quote           *GenerationCost     `json:"quote,omitempty"`
	Hold            decimal.Decimal     `json:"hold"`
	Quantity        decimal.Decimal     `json:"quantity"`
	CreatedAt       time.Time           `json:"created_at"`
}

var ErrGenerationPriceUnavailable = errors.New("generation price is unavailable")
var ErrGenerationCostPending = errors.New("upstream final cost is pending")
var ErrGenerationPriceExceedsHold = errors.New("generation price exceeds the authorized hold")

func (r GenerationPriceRule) Validate() error {
	if r.ServiceFee.IsNegative() || r.Minimum.IsNegative() || r.Maximum != nil && (r.Maximum.IsNegative() || r.Maximum.LessThan(r.Minimum)) {
		return errors.New("invalid generation fee bounds")
	}
	switch r.Mode {
	case "fixed":
		if r.UnitPrice == nil || r.UnitPrice.IsNegative() {
			return errors.New("fixed price must be explicitly configured")
		}
		switch r.Unit {
		case "request", "second", "token", "credit":
		default:
			return errors.New("unsupported generation price unit")
		}
	case "cost_plus":
		if len(r.SourceCurrency) != 3 || r.SourceCurrency != strings.ToUpper(r.SourceCurrency) || r.SourceUnit == "" || r.ExchangeRate == nil || !r.ExchangeRate.IsPositive() || r.Markup == nil || r.Markup.IsNegative() {
			return errors.New("cost pricing requires currency, unit, positive conversion and explicit markup")
		}
		if r.SourceUnitPrice != nil && r.SourceUnitPrice.IsNegative() {
			return errors.New("negative upstream unit price")
		}
		if r.SourceUnit == "upstream_cost" && (r.Maximum == nil || !r.Maximum.IsPositive()) {
			return errors.New("upstream final-bill pricing requires a positive maximum hold")
		}
	default:
		return errors.New("unsupported generation price mode")
	}
	return nil
}

func (r GenerationPriceRule) Price(quantity decimal.Decimal, cost *GenerationCost, multiplier decimal.Decimal) (decimal.Decimal, error) {
	if err := r.Validate(); err != nil {
		return decimal.Zero, err
	}
	if quantity.IsNegative() || multiplier.IsNegative() {
		return decimal.Zero, errors.New("negative usage or multiplier")
	}
	var amount decimal.Decimal
	if r.Mode == "fixed" {
		if r.Unit == "request" {
			quantity = decimal.NewFromInt(1)
		}
		amount = r.UnitPrice.Mul(quantity)
	} else {
		if cost == nil {
			return decimal.Zero, ErrGenerationPriceUnavailable
		}
		if cost.Amount.IsNegative() || cost.Currency != r.SourceCurrency || cost.Unit != r.SourceUnit || cost.Source == "" {
			return decimal.Zero, errors.New("upstream cost does not match configured currency and unit")
		}
		amount = cost.Amount.Mul(*r.ExchangeRate).Mul(*r.Markup)
	}
	amount = decimal.Max(r.Minimum, amount.Add(r.ServiceFee)).Mul(multiplier).Round(UsageBillingMonetaryScale)
	if r.Maximum != nil && amount.GreaterThan(r.Maximum.Mul(multiplier).Round(UsageBillingMonetaryScale)) {
		return decimal.Zero, ErrGenerationPriceExceedsHold
	}
	return amount, nil
}

func NewGenerationPriceSnapshot(rule GenerationPriceRule, quantity decimal.Decimal, quote *GenerationCost, multiplier decimal.Decimal) (GenerationPriceSnapshot, error) {
	var amount decimal.Decimal
	var err error
	if rule.Mode == "cost_plus" && rule.SourceUnit == "upstream_cost" {
		if err = rule.Validate(); err != nil {
			return GenerationPriceSnapshot{}, err
		}
		if multiplier.IsNegative() || quantity.IsNegative() {
			return GenerationPriceSnapshot{}, errors.New("negative usage or multiplier")
		}
		amount = rule.Maximum.Mul(multiplier).Round(UsageBillingMonetaryScale)
		quote = nil
	} else {
		amount, err = rule.Price(quantity, quote, multiplier)
	}
	if err != nil {
		return GenerationPriceSnapshot{}, err
	}
	// Detach pointer-backed inputs so edits cannot change a running task's price.
	raw, err := json.Marshal(rule)
	if err != nil {
		return GenerationPriceSnapshot{}, err
	}
	var frozen GenerationPriceRule
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return GenerationPriceSnapshot{}, err
	}
	var frozenQuote *GenerationCost
	if quote != nil {
		copy := *quote
		frozenQuote = &copy
	}
	return GenerationPriceSnapshot{Rule: frozen, GroupMultiplier: multiplier, Quote: frozenQuote, Hold: amount, Quantity: quantity, CreatedAt: time.Now().UTC()}, nil
}

func (s GenerationPriceSnapshot) Settle(quantity decimal.Decimal, cost *GenerationCost) (decimal.Decimal, error) {
	if s.Rule.Mode == "cost_plus" && (cost == nil || !cost.Final) {
		return decimal.Zero, ErrGenerationCostPending
	}
	amount, err := s.Rule.Price(quantity, cost, s.GroupMultiplier)
	if err != nil {
		return decimal.Zero, err
	}
	if amount.GreaterThan(s.Hold) {
		return decimal.Zero, fmt.Errorf("%w: %s > %s", ErrGenerationPriceExceedsHold, amount, s.Hold)
	}
	return amount, nil
}
