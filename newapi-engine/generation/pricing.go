package generation

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/taskprice"
	"github.com/shopspring/decimal"
)

// PriceRule is an operator-controlled rule. All amounts settle in
// the site's USD ledger. Currency/unit conversion is explicit and immutable.
type PriceRule struct {
	Mode            string           `json:"mode"` // fixed / cost_plus / expression
	Expression      string           `json:"expression,omitempty"`
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

type Cost struct {
	Amount     decimal.Decimal `json:"amount"`
	Currency   string          `json:"currency"`
	Unit       string          `json:"unit"`
	Final      bool            `json:"final"`
	Source     string          `json:"source"`
	ObservedAt time.Time       `json:"observed_at"`
	ExpiresAt  *time.Time      `json:"expires_at,omitempty"`
}

// QuoteComparison records the variance between a frozen pre-submit quote
// and the final observed upstream cost / settled sale amount for admin audits.
type QuoteComparison struct {
	QuotedCost       decimal.Decimal  `json:"quoted_cost"`
	QuotedCurrency   string           `json:"quoted_currency"`
	QuotedUnit       string           `json:"quoted_unit"`
	QuotedSource     string           `json:"quoted_source"`
	QuotedSaleAmount *decimal.Decimal `json:"quoted_sale_amount,omitempty"`
	FinalCost        *decimal.Decimal `json:"final_cost,omitempty"`
	FinalCurrency    string           `json:"final_currency,omitempty"`
	FinalUnit        string           `json:"final_unit,omitempty"`
	FinalSource      string           `json:"final_source,omitempty"`
	ActualSaleAmount *decimal.Decimal `json:"actual_sale_amount,omitempty"`
	CostDelta        *decimal.Decimal `json:"cost_delta,omitempty"`
	SaleDelta        *decimal.Decimal `json:"sale_delta,omitempty"`
}

type PriceSnapshot struct {
	Rule            PriceRule           `json:"rule"`
	GroupMultiplier decimal.Decimal     `json:"group_multiplier"`
	Quote           *Cost               `json:"quote,omitempty"`
	Hold            decimal.Decimal     `json:"hold"`
	Quantity        decimal.Decimal     `json:"quantity"`
	CreatedAt       time.Time           `json:"created_at"`
	Expression      *ExpressionSnapshot `json:"expression,omitempty"`
}

var ErrPriceUnavailable = errors.New("generation price is unavailable")
var ErrCostPending = errors.New("upstream final cost is pending")
var ErrPriceExceedsHold = errors.New("generation price exceeds the authorized hold")
var ErrQuoteExpired = errors.New("upstream price quote has expired")

func (r PriceRule) UsesFinalBill() bool {
	return r.Mode == "cost_plus" && r.SourceUnit == "upstream_cost"
}

func (r PriceRule) Validate() error {
	if r.UsesFinalBill() && (r.Maximum == nil || !boundedFinalBillAmount(*r.Maximum) || !boundedFinalBillAmount(r.ServiceFee) || !boundedFinalBillAmount(r.Minimum)) {
		return errors.New("upstream final-bill pricing requires bounded fee amounts and a maximum hold")
	}
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
		if r.UsesFinalBill() && (!boundedFinalBillAmount(*r.ExchangeRate) || !boundedFinalBillAmount(*r.Markup)) {
			return errors.New("upstream final-bill pricing amount exceeds limit")
		}
	case "expression":
		if r.Maximum == nil || !r.Maximum.IsPositive() {
			return errors.New("task expression pricing requires a positive maximum hold")
		}
		if _, err := taskprice.Compile(r.Expression); err != nil {
			return err
		}
	default:
		return errors.New("unsupported generation price mode")
	}
	return nil
}

func (r PriceRule) Price(quantity decimal.Decimal, cost *Cost, multiplier decimal.Decimal) (decimal.Decimal, error) {
	if err := r.Validate(); err != nil {
		return decimal.Zero, err
	}
	if quantity.IsNegative() || multiplier.IsNegative() {
		return decimal.Zero, errors.New("negative usage or multiplier")
	}
	var amount decimal.Decimal
	if r.Mode == "expression" {
		return decimal.Zero, errors.New("task expression pricing requires a frozen usage schema and facts")
	}
	if r.Mode == "fixed" {
		if r.Unit == "request" {
			quantity = decimal.NewFromInt(1)
		}
		amount = r.UnitPrice.Mul(quantity)
	} else {
		if cost == nil {
			return decimal.Zero, ErrPriceUnavailable
		}
		if cost.Amount.IsNegative() || cost.Currency != r.SourceCurrency || cost.Unit != r.SourceUnit || cost.Source == "" {
			return decimal.Zero, errors.New("upstream cost does not match configured currency and unit")
		}
		amount = cost.Amount.Mul(*r.ExchangeRate).Mul(*r.Markup)
	}
	return r.priceTotal(amount, multiplier)
}

func (r PriceRule) priceTotal(amount, multiplier decimal.Decimal) (decimal.Decimal, error) {
	if amount.IsNegative() || multiplier.IsNegative() {
		return decimal.Zero, errors.New("negative generation price or multiplier")
	}
	amount = decimal.Max(r.Minimum, amount.Add(r.ServiceFee)).Mul(multiplier).Round(MonetaryScale)
	if r.Maximum != nil && amount.GreaterThan(r.Maximum.Mul(multiplier).Round(MonetaryScale)) {
		return decimal.Zero, ErrPriceExceedsHold
	}
	return amount, nil
}

// ValidateQuote verifies that a pre-submit upstream quote matches the rule's
// currency and unit, carries bounded non-negative cost, and has not expired.
func (r PriceRule) ValidateQuote(quote *Cost, now time.Time) error {
	if r.Mode != "cost_plus" || r.Validate() != nil || quote == nil {
		return ErrPriceUnavailable
	}
	if !boundedFinalBillAmount(quote.Amount) || quote.Currency != r.SourceCurrency || quote.Unit != r.SourceUnit || strings.TrimSpace(quote.Source) == "" || len(quote.Source) > 128 {
		return errors.New("upstream cost does not match configured currency and unit")
	}
	if quote.ObservedAt.IsZero() {
		return errors.New("upstream quote requires observed_at")
	}
	if quote.ExpiresAt != nil {
		if quote.ExpiresAt.IsZero() || !quote.ExpiresAt.After(quote.ObservedAt) {
			return ErrQuoteExpired
		}
		if !now.IsZero() && !quote.ExpiresAt.After(now) {
			return ErrQuoteExpired
		}
	}
	return nil
}

func NewPriceSnapshot(rule PriceRule, quantity decimal.Decimal, quote *Cost, multiplier decimal.Decimal) (PriceSnapshot, error) {
	return NewPriceSnapshotAt(rule, quantity, quote, multiplier, time.Now().UTC())
}

func NewPriceSnapshotAt(rule PriceRule, quantity decimal.Decimal, quote *Cost, multiplier decimal.Decimal, at time.Time) (PriceSnapshot, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var amount decimal.Decimal
	var err error
	if rule.UsesFinalBill() {
		if err = rule.Validate(); err != nil {
			return PriceSnapshot{}, err
		}
		if multiplier.IsNegative() || quantity.IsNegative() {
			return PriceSnapshot{}, errors.New("negative usage or multiplier")
		}
		amount = rule.Maximum.Mul(multiplier).Round(MonetaryScale)
		if quote != nil {
			if err = rule.ValidateQuote(quote, at); err != nil {
				return PriceSnapshot{}, err
			}
			quoteBase := decimal.Max(rule.Minimum, quote.Amount.Mul(*rule.ExchangeRate).Mul(*rule.Markup).Add(rule.ServiceFee))
			if quoteBase.GreaterThan(*rule.Maximum) {
				return PriceSnapshot{}, ErrPriceExceedsHold
			}
		}
	} else {
		if rule.Mode == "cost_plus" {
			if err = rule.ValidateQuote(quote, at); err != nil {
				return PriceSnapshot{}, err
			}
		}
		amount, err = rule.Price(quantity, quote, multiplier)
	}
	if err != nil {
		return PriceSnapshot{}, err
	}
	// Detach pointer-backed inputs so edits cannot change a running task's price.
	raw, err := json.Marshal(rule)
	if err != nil {
		return PriceSnapshot{}, err
	}
	var frozen PriceRule
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return PriceSnapshot{}, err
	}
	var frozenQuote *Cost
	if quote != nil {
		copy := *quote
		copy.Final = false // A pre-submit quote is never a settled upstream invoice.
		if quote.ExpiresAt != nil {
			exp := quote.ExpiresAt.UTC()
			copy.ExpiresAt = &exp
		}
		frozenQuote = &copy
	}
	return PriceSnapshot{Rule: frozen, GroupMultiplier: multiplier, Quote: frozenQuote, Hold: amount, Quantity: quantity, CreatedAt: at.UTC()}, nil
}

func (s PriceSnapshot) CompareQuote(finalCost *Cost, actualSale *decimal.Decimal) *QuoteComparison {
	if s.Quote == nil {
		return nil
	}
	cmp := &QuoteComparison{
		QuotedCost:     s.Quote.Amount,
		QuotedCurrency: s.Quote.Currency,
		QuotedUnit:     s.Quote.Unit,
		QuotedSource:   s.Quote.Source,
	}
	if quotedSale, err := s.Rule.Price(s.Quantity, s.Quote, s.GroupMultiplier); err == nil {
		cmp.QuotedSaleAmount = &quotedSale
	}
	if finalCost != nil {
		fc := finalCost.Amount
		cmp.FinalCost = &fc
		cmp.FinalCurrency = finalCost.Currency
		cmp.FinalUnit = finalCost.Unit
		cmp.FinalSource = finalCost.Source
		if finalCost.Currency == s.Quote.Currency && finalCost.Unit == s.Quote.Unit {
			delta := finalCost.Amount.Sub(s.Quote.Amount)
			cmp.CostDelta = &delta
		}
	}
	if actualSale != nil {
		as := *actualSale
		cmp.ActualSaleAmount = &as
		if cmp.QuotedSaleAmount != nil {
			sd := actualSale.Sub(*cmp.QuotedSaleAmount)
			cmp.SaleDelta = &sd
		}
	}
	return cmp
}

func (s PriceSnapshot) Settle(quantity decimal.Decimal, cost *Cost) (decimal.Decimal, error) {
	if s.Rule.Mode == "cost_plus" && (cost == nil || !cost.Final) {
		return decimal.Zero, ErrCostPending
	}
	if s.Rule.UsesFinalBill() {
		base, err := s.FinalBillBase(cost)
		if err != nil {
			return decimal.Zero, err
		}
		if quantity.IsNegative() || s.GroupMultiplier.IsNegative() {
			return decimal.Zero, errors.New("negative usage or multiplier")
		}
		amount := base.Mul(s.GroupMultiplier).Round(MonetaryScale)
		if amount.GreaterThan(s.Hold) {
			return decimal.Zero, ErrPriceExceedsHold
		}
		return amount, nil
	}
	amount, err := s.Rule.Price(quantity, cost, s.GroupMultiplier)
	if err != nil {
		return decimal.Zero, err
	}
	if amount.GreaterThan(s.Hold) {
		return decimal.Zero, fmt.Errorf("%w: %s > %s", ErrPriceExceedsHold, amount, s.Hold)
	}
	return amount, nil
}

// FinalBillBase is the verified USD charge prior to either user or account
// multipliers. Check the unrounded charge against the frozen ceiling even
// when the user multiplier is zero and the user hold has no balance impact.
func (s PriceSnapshot) FinalBillBase(cost *Cost) (decimal.Decimal, error) {
	r := s.Rule
	if !r.UsesFinalBill() || r.Validate() != nil {
		return decimal.Zero, ErrPriceUnavailable
	}
	if cost == nil || !cost.Final {
		return decimal.Zero, ErrCostPending
	}
	if !boundedFinalBillAmount(cost.Amount) || cost.Currency != r.SourceCurrency || cost.Unit != r.SourceUnit || cost.Source == "" || len(cost.Source) > 128 {
		return decimal.Zero, errors.New("upstream cost does not match configured currency and unit")
	}
	base := decimal.Max(r.Minimum, cost.Amount.Mul(*r.ExchangeRate).Mul(*r.Markup).Add(r.ServiceFee))
	if base.GreaterThan(*r.Maximum) {
		return decimal.Zero, ErrPriceExceedsHold
	}
	return base, nil
}
