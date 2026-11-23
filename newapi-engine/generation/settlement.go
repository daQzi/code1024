package generation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

func (e *Executor) Settle(ctx context.Context, task *Task) error {
	if e.store == nil {
		return errors.New("generation task store unavailable")
	}
	if task == nil || task.Status != Succeeded && task.Status != Failed {
		return errors.New("generation settlement requires a terminal task")
	}
	if task.BillingStatus == BillingSettled {
		return nil
	}
	quantity := task.Price.Quantity
	unit := task.Price.Rule.Unit
	if task.Price.Rule.Mode == "cost_plus" {
		unit = task.Price.Rule.SourceUnit
	}
	if value, ok := Quantity(task.Usage, unit); ok {
		quantity = value
	}
	var finalFacts map[string]json.RawMessage
	_ = json.Unmarshal(task.Usage, &finalFacts)
	finalQuantity, observed := Quantity(finalFacts["_host_final_usage"], unit)
	if observed {
		quantity = finalQuantity
	}
	var cost *Cost
	if task.Price.Rule.Mode == "cost_plus" && unit == "upstream_cost" {
		cost = ObservedCost(finalFacts["_host_final_usage"])
		observed = cost != nil
	} else if task.Price.Rule.Mode == "cost_plus" {
		if obs := ObservedCost(finalFacts["_host_final_usage"]); obs != nil {
			cost = obs
			observed = true
		} else if task.Price.Rule.SourceUnitPrice != nil {
			cost = &Cost{Amount: quantity.Mul(*task.Price.Rule.SourceUnitPrice), Currency: task.Price.Rule.SourceCurrency, Unit: unit, Final: observed, Source: "configured_upstream_price", ObservedAt: e.now().UTC()}
		} else {
			observed = false
		}
	}
	actual, err := task.Price.Settle(quantity, cost)
	if task.Price.Rule.Mode == "expression" {
		charge, expressionErr := task.Price.SettleExpression(finalFacts["_host_final_usage"])
		err, observed = expressionErr, expressionErr == nil
		if expressionErr == nil {
			actual = charge.Amount
		}
	}
	if !observed {
		err = ErrCostPending
	}
	// FAILURE means the driver confirmed a non-billable failure. Drivers such as
	// xAI represent charged moderation as SUCCESS with a delivery notice.
	if task.Status == Failed {
		actual = decimal.Zero
		err = nil
		if task.ErrorCode == CanceledBeforeSubmit || task.ErrorCode == CanceledUpstream {
			cost = nil // A confirmed cancel does not settle a generation charge.
		}
	}
	if task.ReconciledAmount != nil && task.ErrorCode != CanceledBeforeSubmit && task.ErrorCode != CanceledUpstream {
		actual = *task.ReconciledAmount
		err = nil
	}
	if err != nil {
		task.BillingStatus = BillingReview
		task.ErrorCode = "billing_review"
		task.ErrorMessage = "Final usage requires administrator review."
		return e.store.SaveProgress(ctx, task, time.Hour)
	}
	if err = e.store.Settle(ctx, task, actual, cost); err != nil {
		return err
	}
	if task.BillingStatus != BillingSettled {
		return errors.New("generation settlement did not confirm its commit")
	}
	if e.afterSettlement != nil {
		e.afterSettlement(ctx, task, actual)
	}
	return nil
}

func Quantity(facts json.RawMessage, unit string) (decimal.Decimal, bool) {
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
func BillingPendingFor(task *Task) bool {
	if task.ReconciledAmount != nil || task.Status != Succeeded {
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

func ObservedCost(raw json.RawMessage) *Cost {
	if len(raw) > 1<<20 {
		return nil
	}
	var facts struct {
		Cost *struct {
			Amount   json.RawMessage `json:"amount"`
			Currency string          `json:"currency"`
			Unit     string          `json:"unit"`
			Final    bool            `json:"final"`
			Source   string          `json:"source"`
		} `json:"upstream_cost"`
	}
	if json.Unmarshal(raw, &facts) != nil || facts.Cost == nil || len(facts.Cost.Amount) == 0 || len(facts.Cost.Amount) > 128 || string(facts.Cost.Amount) == "null" {
		return nil
	}
	cost := facts.Cost
	var amount decimal.Decimal
	if !cost.Final || cost.Source == "" || len(cost.Source) > 128 || json.Unmarshal(cost.Amount, &amount) != nil || !boundedFinalBillAmount(amount) {
		return nil
	}
	return &Cost{Amount: amount, Currency: cost.Currency, Unit: cost.Unit, Final: true, Source: cost.Source, ObservedAt: time.Now().UTC()}
}

func boundedFinalBillAmount(amount decimal.Decimal) bool {
	return amount.Exponent() >= -32 && amount.Exponent() <= 32 && !amount.IsNegative() && amount.LessThanOrEqual(decimal.NewFromInt(1e12))
}
