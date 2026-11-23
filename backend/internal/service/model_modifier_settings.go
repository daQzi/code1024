package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"golang.org/x/sync/singleflight"
)

const modelModifierSettingKey = "plugin_model_modifier_exemptions"

var ErrModelModifierConflict = infraerrors.Conflict("MODEL_MODIFIER_CONFLICT", "模型名豁免已被修改，请刷新后重试")

var errModelModifierRefreshSuperseded = errors.New("model modifier refresh superseded by a save")

type ModelModifierSettings struct {
	Exemptions        []string `json:"exemptions"`
	UseStartup        bool     `json:"use_startup"`
	Revision          string   `json:"revision"`
	StartupExemptions []string `json:"startup_exemptions"`
}

type modelModifierStored struct {
	Exemptions []string `json:"exemptions"`
	UseStartup bool     `json:"use_startup"`
}

type modelModifierSettingWriter interface {
	CompareAndSwapSetting(context.Context, string, *string, string) (bool, error)
}

type modelModifierSettingsCache struct {
	mu         sync.Mutex
	value      ModelModifierSettings
	expires    time.Time
	generation uint64
	refresh    singleflight.Group
}

func cloneModelModifierSettings(value ModelModifierSettings) ModelModifierSettings {
	value.Exemptions = slices.Clone(value.Exemptions)
	value.StartupExemptions = slices.Clone(value.StartupExemptions)
	return value
}

func (s *NewAPIPluginService) modelModifierStartup() []string {
	if s.gateway != nil && s.gateway.cfg != nil {
		return append([]string{}, s.gateway.cfg.Gateway.ModelModifierExemptions...)
	}
	return []string{}
}

func (s *NewAPIPluginService) readModelModifierSettings(ctx context.Context) (ModelModifierSettings, *string, error) {
	if s.marketSettings == nil {
		return ModelModifierSettings{}, nil, errors.New("模型名豁免配置服务不可用")
	}
	value := modelModifierStored{UseStartup: true}
	raw, err := s.marketSettings.GetValue(ctx, modelModifierSettingKey)
	var previous *string
	if errors.Is(err, ErrSettingNotFound) {
		err = nil
	} else if err == nil {
		var stored struct {
			Exemptions []string `json:"exemptions"`
			UseStartup *bool    `json:"use_startup"`
		}
		if len(raw) > 512<<10 || json.Unmarshal([]byte(raw), &stored) != nil || stored.UseStartup == nil || !*stored.UseStartup && stored.Exemptions == nil {
			return ModelModifierSettings{}, nil, errors.New("模型名豁免配置无效")
		}
		value = modelModifierStored{Exemptions: stored.Exemptions, UseStartup: *stored.UseStartup}
		previous = &raw
	}
	if err != nil {
		return ModelModifierSettings{}, nil, err
	}
	startup := s.modelModifierStartup()
	entries := value.Exemptions
	if value.UseStartup {
		entries = startup
	}
	if _, err := modelmodifier.CompilePolicy(entries); err != nil {
		return ModelModifierSettings{}, nil, errors.New("模型名豁免配置无效")
	}
	encoded, _ := json.Marshal(struct {
		Entries []string
		Startup bool
	}{entries, value.UseStartup})
	return ModelModifierSettings{Exemptions: append([]string{}, entries...), UseStartup: value.UseStartup, Revision: HashUsageRequestPayload(encoded), StartupExemptions: startup}, previous, nil
}

// All gateway model readers share the same immutable atomic policy. Refresh
// happens before allowlists and routing, with one bounded read per node/TTL.
// Failed/invalid refreshes reject admission; they do not restore permissive
// defaults or replace the last valid policy. Active Provider exchanges already
// carry their resolved modifier and billing identity.
func (s *NewAPIPluginService) loadModelModifierSettings(ctx context.Context, force bool) (ModelModifierSettings, error) {
	cache := &s.modelModifiers
	for {
		cache.mu.Lock()
		if !force && time.Now().Before(cache.expires) {
			value := cloneModelModifierSettings(cache.value)
			cache.mu.Unlock()
			return value, nil
		}
		cache.mu.Unlock()
		pending := cache.refresh.DoChan("policy", func() (any, error) {
			cache.mu.Lock()
			generation := cache.generation
			cache.mu.Unlock()
			readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			value, _, err := s.readModelModifierSettings(readCtx)
			cache.mu.Lock()
			defer cache.mu.Unlock()
			if generation != cache.generation {
				return nil, errModelModifierRefreshSuperseded
			}
			if err != nil {
				return nil, err
			}
			if err := modelmodifier.Configure(value.Exemptions); err != nil {
				return nil, err
			}
			cache.value, cache.expires = cloneModelModifierSettings(value), time.Now().Add(2*time.Second)
			return cloneModelModifierSettings(value), nil
		})
		select {
		case <-ctx.Done():
			return ModelModifierSettings{}, ctx.Err()
		case result := <-pending:
			if errors.Is(result.Err, errModelModifierRefreshSuperseded) {
				// Never return an old or uninitialized value while the save's
				// authoritative refresh is still in flight.
				force = false
				continue
			}
			if result.Err != nil {
				return ModelModifierSettings{}, result.Err
			}
			return cloneModelModifierSettings(result.Val.(ModelModifierSettings)), nil
		}
	}
}

func (s *NewAPIPluginService) EnsureModelModifierPolicy(ctx context.Context) error {
	if s == nil || s.marketSettings == nil {
		return nil // Unbound test/legacy handlers retain the validated startup policy.
	}
	_, err := s.loadModelModifierSettings(ctx, false)
	return err
}

func (s *NewAPIPluginService) ModelModifierSettings(ctx context.Context) (ModelModifierSettings, error) {
	return s.loadModelModifierSettings(ctx, true)
}

func (s *NewAPIPluginService) SaveModelModifierSettings(ctx context.Context, input ModelModifierSettings) (ModelModifierSettings, error) {
	writer, ok := s.marketSettings.(modelModifierSettingWriter)
	if !ok {
		return ModelModifierSettings{}, errors.New("模型名豁免配置服务不可用")
	}
	if !input.UseStartup && input.Exemptions == nil {
		return ModelModifierSettings{}, infraerrors.BadRequest("INVALID_MODEL_MODIFIER_POLICY", "请明确填写模型名豁免列表，空列表表示无豁免")
	}
	if _, err := modelmodifier.CompilePolicy(input.Exemptions); err != nil {
		return ModelModifierSettings{}, infraerrors.BadRequest("INVALID_MODEL_MODIFIER_POLICY", err.Error())
	}
	current, previous, err := s.readModelModifierSettings(ctx)
	if err != nil {
		return ModelModifierSettings{}, err
	}
	if input.Revision == "" || input.Revision != current.Revision {
		return ModelModifierSettings{}, ErrModelModifierConflict
	}
	stored := modelModifierStored{UseStartup: input.UseStartup, Exemptions: append([]string{}, input.Exemptions...)}
	if input.UseStartup {
		stored.Exemptions = []string{}
		input.Exemptions = s.modelModifierStartup()
		if _, err := modelmodifier.CompilePolicy(input.Exemptions); err != nil {
			return ModelModifierSettings{}, infraerrors.BadRequest("INVALID_MODEL_MODIFIER_POLICY", err.Error())
		}
	}
	raw, _ := json.Marshal(stored)
	updated, err := writer.CompareAndSwapSetting(ctx, modelModifierSettingKey, previous, string(raw))
	if err != nil {
		return ModelModifierSettings{}, err
	}
	if !updated {
		return ModelModifierSettings{}, ErrModelModifierConflict
	}
	// The database write is authoritative. Do not install the browser's input
	// if another node won a subsequent update; reload the confirmed DB state.
	s.modelModifiers.mu.Lock()
	s.modelModifiers.generation++
	s.modelModifiers.expires = time.Time{}
	s.modelModifiers.refresh.Forget("policy")
	s.modelModifiers.mu.Unlock()
	return s.loadModelModifierSettings(ctx, true)
}
