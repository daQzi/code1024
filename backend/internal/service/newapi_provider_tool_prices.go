package service

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/newapi-engine/provider"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/singleflight"
)

const providerToolPriceSettingKey = "newapi_provider_tool_prices"

var ErrProviderToolPricesConflict = infraerrors.Conflict("PROVIDER_TOOL_PRICES_CONFLICT", "工具售价已被修改，请刷新后重试")
var errProviderToolPricesSuperseded = errors.New("provider tool price read superseded by a save")

// Prices are operator sale prices in USD/1K calls, not vendor currency/cost.
// The tool:model-prefix* key syntax matches the pinned New API contract.
type ProviderToolPriceSettings struct {
	Prices   map[string]float64 `json:"prices"`
	Revision string             `json:"revision"`
}

type providerToolPricesSnapshot struct {
	settings ProviderToolPriceSettings
	book     *provider.PriceBook
}

type providerToolPriceCache struct {
	mu         sync.Mutex
	value      providerToolPricesSnapshot
	expires    time.Time
	generation uint64
	refresh    singleflight.Group
}

func (v providerToolPricesSnapshot) clone() providerToolPricesSnapshot {
	v.settings.Prices = maps.Clone(v.settings.Prices)
	return v // The compiled price book is immutable.
}

func (s *NewAPIPluginService) readProviderToolPrices(ctx context.Context) (providerToolPricesSnapshot, *string, error) {
	if s == nil || s.marketSettings == nil {
		return providerToolPricesSnapshot{}, nil, errors.New("工具售价配置服务不可用")
	}
	value := ProviderToolPriceSettings{Prices: map[string]float64{}}
	raw, err := s.marketSettings.GetValue(ctx, providerToolPriceSettingKey)
	var previous *string
	if errors.Is(err, ErrSettingNotFound) {
		err = nil
	} else if err == nil {
		value.Prices = nil
		if len(raw) > 256<<10 || !gjson.Valid(raw) || !newAPIProviderUnambiguousJSON(gjson.Parse(raw), 0) || json.Unmarshal([]byte(raw), &value) != nil || value.Prices == nil {
			return providerToolPricesSnapshot{}, nil, errors.New("工具售价配置无效")
		}
		previous = &raw
	}
	if err != nil {
		return providerToolPricesSnapshot{}, nil, err
	}
	book, err := provider.NewPriceBook(value.Prices)
	if err != nil {
		return providerToolPricesSnapshot{}, nil, errors.New("工具售价配置无效")
	}
	encoded, _ := json.Marshal(value.Prices)
	value.Revision = HashUsageRequestPayload(encoded)
	return providerToolPricesSnapshot{settings: value, book: book}, previous, nil
}

func (s *NewAPIPluginService) loadProviderToolPrices(ctx context.Context, force bool) (providerToolPricesSnapshot, error) {
	cache := &s.providerToolPrices
	for {
		cache.mu.Lock()
		if !force && time.Now().Before(cache.expires) {
			value := cache.value.clone()
			cache.mu.Unlock()
			return value, nil
		}
		cache.mu.Unlock()
		pending := cache.refresh.DoChan("prices", func() (any, error) {
			cache.mu.Lock()
			generation := cache.generation
			cache.mu.Unlock()
			readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			value, _, err := s.readProviderToolPrices(readCtx)
			cache.mu.Lock()
			defer cache.mu.Unlock()
			if generation != cache.generation {
				return nil, errProviderToolPricesSuperseded
			}
			if err != nil {
				return nil, err
			}
			cache.value, cache.expires = value.clone(), time.Now().Add(2*time.Second)
			return value.clone(), nil
		})
		select {
		case <-ctx.Done():
			return providerToolPricesSnapshot{}, ctx.Err()
		case result := <-pending:
			if errors.Is(result.Err, errProviderToolPricesSuperseded) {
				force = false
				continue
			}
			if result.Err != nil {
				return providerToolPricesSnapshot{}, result.Err
			}
			return result.Val.(providerToolPricesSnapshot).clone(), nil
		}
	}
}

func (s *NewAPIPluginService) ProviderToolPrices(ctx context.Context) (ProviderToolPriceSettings, error) {
	if s == nil || s.marketSettings == nil {
		return ProviderToolPriceSettings{}, errors.New("工具售价配置服务不可用")
	}
	value, err := s.loadProviderToolPrices(ctx, true)
	return value.settings, err
}

func (s *NewAPIPluginService) SaveProviderToolPrices(ctx context.Context, input ProviderToolPriceSettings) (ProviderToolPriceSettings, error) {
	if s == nil {
		return ProviderToolPriceSettings{}, errors.New("工具售价配置服务不可用")
	}
	writer, ok := s.marketSettings.(interface {
		CompareAndSwapSetting(context.Context, string, *string, string) (bool, error)
	})
	if !ok {
		return ProviderToolPriceSettings{}, errors.New("工具售价配置服务不可用")
	}
	if input.Prices == nil {
		return ProviderToolPriceSettings{}, infraerrors.BadRequest("INVALID_PROVIDER_TOOL_PRICES", "请明确提供工具售价表，空对象表示移除所有覆盖")
	}
	if _, err := provider.NewPriceBook(input.Prices); err != nil {
		return ProviderToolPriceSettings{}, infraerrors.BadRequest("INVALID_PROVIDER_TOOL_PRICES", err.Error())
	}
	current, previous, err := s.readProviderToolPrices(ctx)
	if err != nil {
		return ProviderToolPriceSettings{}, err
	}
	if input.Revision == "" || input.Revision != current.settings.Revision {
		return ProviderToolPriceSettings{}, ErrProviderToolPricesConflict
	}
	raw, _ := json.Marshal(struct {
		Prices map[string]float64 `json:"prices"`
	}{input.Prices})
	updated, err := writer.CompareAndSwapSetting(ctx, providerToolPriceSettingKey, previous, string(raw))
	if err != nil {
		return ProviderToolPriceSettings{}, err
	}
	if !updated {
		return ProviderToolPriceSettings{}, ErrProviderToolPricesConflict
	}
	cache := &s.providerToolPrices
	cache.mu.Lock()
	cache.generation++
	cache.expires = time.Time{}
	cache.refresh.Forget("prices")
	cache.mu.Unlock()
	return s.ProviderToolPrices(ctx)
}

func (s *NewAPIPluginService) providerToolUsage(ctx context.Context, key *APIKey, model string) (*newAPIProviderToolUsage, error) {
	u := newProviderToolUsage(key)
	u.model = model
	if s.marketSettings == nil {
		return u, nil // Unbound test/legacy hosts retain explicit group search prices.
	}
	value, err := s.loadProviderToolPrices(ctx, false)
	if err != nil {
		return nil, err // Never fall back to stale/free prices after a failed refresh.
	}
	u.prices = value.book
	return u, nil
}
