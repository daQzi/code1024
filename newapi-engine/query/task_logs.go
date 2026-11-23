// Package query exposes console projections independently of task execution.
// A host may aggregate read-only historical sources without giving this
// service a submit, claim or financial port.
package query

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/taskprice"
	"github.com/shopspring/decimal"
)

// Log is the console projection, not a serialized execution snapshot.
// Credentials, plugin state, costs and rewritten requests never enter it.
type Log struct {
	ID               string           `json:"id"`
	UserID           int64            `json:"user_id,omitempty"`
	APIKeyID         int64            `json:"-"`
	GroupID          int64            `json:"group_id"`
	GroupName        string           `json:"group_name"`
	AccountID        int64            `json:"account_id,omitempty"`
	UpstreamID       string           `json:"upstream_id,omitempty"`
	Model            string           `json:"model"`
	Platform         string           `json:"platform"`
	Protocol         string           `json:"protocol"`
	Action           string           `json:"action"`
	PluginKey        string           `json:"plugin_key"`
	PluginVersion    string           `json:"plugin_version"`
	Source           string           `json:"source"`
	MonitorStatus    string           `json:"monitor_status,omitempty"`
	MonitorErrorCode string           `json:"monitor_error_code,omitempty"`
	Status           string           `json:"status"`
	DeliveryStatus   string           `json:"delivery_status"`
	BillingStatus    string           `json:"billing_status"`
	Progress         int              `json:"progress"`
	HoldAmount       decimal.Decimal  `json:"hold_amount"`
	ActualAmount     *decimal.Decimal `json:"actual_amount"`
	ErrorCode        string           `json:"error_code,omitempty"`
	ErrorMessage     string           `json:"error_message,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
	FinishedAt       *time.Time       `json:"finished_at"`
}

type Event struct {
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}

type BillingSummary struct {
	Mode            string                       `json:"mode,omitempty"`
	GroupMultiplier *decimal.Decimal             `json:"group_multiplier,omitempty"`
	MatchedTier     string                       `json:"matched_tier,omitempty"`
	RequestRules    []taskprice.RequestRuleTrace `json:"request_rules,omitempty"`
	QuoteComparison *generation.QuoteComparison  `json:"quote_comparison,omitempty"`
}

// Preserve the embedded field name used by existing storage adapters.
type TaskLog = Log

type Detail struct {
	TaskLog
	Fields        generation.VideoFields `json:"fields"`
	Events        []Event                `json:"events"`
	NativeActions []string               `json:"native_actions,omitempty"`
	Billing       *BillingSummary        `json:"billing,omitempty"`
}

type Filter struct {
	UserID, GroupID, AccountID int64
	TaskID, Model, PluginKey   string
	Status, BillingStatus      string
	Start, End                 *time.Time
	ReviewOnly                 bool
	Page, PageSize             int
}

// Separate from the executor contract so console queries do not change worker
// admission, task ownership or any settlement operation.
type Repository interface {
	ListTaskLogs(context.Context, Filter) ([]Log, int64, error)
	GetTaskLog(context.Context, string, int64) (*Detail, error)
}

// Access must come from authenticated host identity, never a request filter.
type Access struct {
	UserID        int64
	Administrator bool
}
type Service struct{ repo Repository }

func NewService(repo Repository) *Service { return &Service{repo: repo} }

func (s *Service) List(ctx context.Context, access Access, filter Filter) ([]Log, int64, error) {
	if s.repo == nil {
		return nil, 0, errors.New("task log repository unavailable")
	}
	if !access.Administrator {
		if access.UserID <= 0 {
			return nil, 0, generation.ErrTaskNotFound
		}
		filter.UserID = access.UserID
		filter.AccountID = 0
	}
	items, total, err := s.repo.ListTaskLogs(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	if !access.Administrator {
		items = slices.Clone(items)
		for i := range items {
			items[i] = publicLog(items[i])
		}
	}
	return items, total, nil
}

func (s *Service) Get(ctx context.Context, access Access, id string) (*Detail, error) {
	if s.repo == nil {
		return nil, errors.New("task log repository unavailable")
	}
	if !access.Administrator && access.UserID <= 0 {
		return nil, generation.ErrTaskNotFound
	}
	item, err := s.repo.GetTaskLog(ctx, id, access.UserID)
	if err != nil {
		return nil, err
	}
	if item == nil || !access.Administrator && item.UserID != access.UserID {
		return nil, generation.ErrTaskNotFound
	}
	copy := *item
	copy.Events = slices.Clone(item.Events)
	copy.NativeActions = slices.Clone(item.NativeActions)
	if !access.Administrator {
		copy.TaskLog = publicLog(copy.TaskLog)
		copy.NativeActions = nil
		copy.Billing = nil
	} else if item.Billing != nil {
		billingCopy := *item.Billing
		billingCopy.RequestRules = slices.Clone(item.Billing.RequestRules)
		if item.Billing.GroupMultiplier != nil {
			m := *item.Billing.GroupMultiplier
			billingCopy.GroupMultiplier = &m
		}
		if item.Billing.QuoteComparison != nil {
			qc := *item.Billing.QuoteComparison
			billingCopy.QuoteComparison = &qc
		}
		copy.Billing = &billingCopy
	}
	return &copy, nil
}

func publicLog(item Log) Log { item.UserID = 0; item.AccountID = 0; item.UpstreamID = ""; return item }
