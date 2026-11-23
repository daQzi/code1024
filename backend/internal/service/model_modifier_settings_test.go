//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/stretchr/testify/require"
)

type modelModifierSettingsFixture struct {
	SettingRepository
	mu      sync.Mutex
	raw     *string
	err     error
	loseCAS bool
	writes  int
	reads   int
	onRead  func(context.Context, int) error
}

func (r *modelModifierSettingsFixture) GetValue(ctx context.Context, _ string) (string, error) {
	r.mu.Lock()
	r.reads++
	n, hook, err := r.reads, r.onRead, r.err
	var raw string
	exists := r.raw != nil
	if exists {
		raw = *r.raw
	}
	r.mu.Unlock()
	if hook != nil {
		if hookErr := hook(ctx, n); hookErr != nil {
			return "", hookErr
		}
	}
	if err != nil {
		return "", err
	}
	if !exists {
		return "", ErrSettingNotFound
	}
	return raw, nil
}

func (r *modelModifierSettingsFixture) CompareAndSwapSetting(_ context.Context, _ string, previous *string, value string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return false, r.err
	}
	if r.loseCAS || (previous == nil) != (r.raw == nil) || previous != nil && *previous != *r.raw {
		return false, nil
	}
	r.raw = &value
	r.writes++
	return true, nil
}

func modelModifierServiceFixture(t *testing.T, startup ...string) (*NewAPIPluginService, *modelModifierSettingsFixture) {
	t.Helper()
	require.NoError(t, modelmodifier.Configure(startup))
	t.Cleanup(func() { require.NoError(t, modelmodifier.Configure(nil)) })
	r := &modelModifierSettingsFixture{}
	s := &NewAPIPluginService{marketSettings: r, gateway: &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{ModelModifierExemptions: startup}}}}
	return s, r
}

func expireModelModifierCache(s *NewAPIPluginService) {
	s.modelModifiers.mu.Lock()
	s.modelModifiers.expires = time.Time{}
	s.modelModifiers.mu.Unlock()
}

func TestModelModifierSettingsStartupEmptyOverrideAndRestore(t *testing.T) {
	s, r := modelModifierServiceFixture(t, "real@effort:high")
	ctx := context.Background()
	value, err := s.ModelModifierSettings(ctx)
	require.NoError(t, err)
	require.True(t, value.UseStartup)
	require.Equal(t, []string{"real@effort:high"}, value.Exemptions)
	require.Equal(t, value.Exemptions, value.StartupExemptions)
	require.Equal(t, "real@effort:high", modelmodifier.Base("real@effort:high"))
	value.Exemptions = []string{}
	value.UseStartup = false
	value, err = s.SaveModelModifierSettings(ctx, value)
	require.NoError(t, err)
	require.False(t, value.UseStartup)
	require.NotNil(t, value.Exemptions)
	require.Empty(t, value.Exemptions)
	require.Equal(t, "real", modelmodifier.Base("real@effort:high"))
	value.UseStartup = true
	value, err = s.SaveModelModifierSettings(ctx, value)
	require.NoError(t, err)
	require.True(t, value.UseStartup)
	require.Equal(t, "real@effort:high", modelmodifier.Base("real@effort:high"))
	r.mu.Lock()
	require.Equal(t, `{"exemptions":[],"use_startup":true}`, *r.raw)
	r.mu.Unlock()
}

func TestModelModifierSettingsExactRegexAndDefensiveCopies(t *testing.T) {
	s, _ := modelModifierServiceFixture(t, "startup@effort:low")
	ctx := context.Background()
	value, err := s.ModelModifierSettings(ctx)
	require.NoError(t, err)
	value.UseStartup = false
	value.Exemptions = []string{"real@effort:high", `re:^vendor/.+@temperature:`}
	saved, err := s.SaveModelModifierSettings(ctx, value)
	require.NoError(t, err)
	require.Equal(t, "real@effort:high", modelmodifier.Base("real@effort:high"))
	require.Equal(t, "vendor/v1@temperature:0.2", modelmodifier.Base("vendor/v1@temperature:0.2"))
	require.Equal(t, "other", modelmodifier.Base("other@temperature:0.2"))
	value.Exemptions[0] = "mutated input"
	saved.Exemptions[0] = "mutated output"
	saved.StartupExemptions[0] = "mutated startup output"
	read, err := s.loadModelModifierSettings(ctx, false)
	require.NoError(t, err)
	require.Equal(t, "real@effort:high", read.Exemptions[0])
	require.Equal(t, "startup@effort:low", read.StartupExemptions[0])
}

func TestModelModifierSettingsRejectsInvalidAndStaleWrites(t *testing.T) {
	s, r := modelModifierServiceFixture(t)
	ctx := context.Background()
	initial, err := s.ModelModifierSettings(ctx)
	require.NoError(t, err)
	for _, entries := range [][]string{nil, {"re:["}, {" spaced "}, {strings.Repeat("界", 171)}, make([]string, 129)} {
		_, err = s.SaveModelModifierSettings(ctx, ModelModifierSettings{Revision: initial.Revision, Exemptions: entries})
		require.Error(t, err)
	}
	require.Zero(t, r.writes)
	_, err = s.SaveModelModifierSettings(ctx, ModelModifierSettings{Revision: "stale", Exemptions: []string{}})
	require.ErrorIs(t, err, ErrModelModifierConflict)
	r.loseCAS = true
	_, err = s.SaveModelModifierSettings(ctx, ModelModifierSettings{Revision: initial.Revision, Exemptions: []string{}})
	require.ErrorIs(t, err, ErrModelModifierConflict)
	require.Zero(t, r.writes)
	r.loseCAS = false
	_, err = s.SaveModelModifierSettings(ctx, ModelModifierSettings{Revision: initial.Revision, Exemptions: []string{"first@effort:high"}})
	require.NoError(t, err)
	_, err = s.SaveModelModifierSettings(ctx, ModelModifierSettings{Revision: initial.Revision, Exemptions: []string{}})
	require.ErrorIs(t, err, ErrModelModifierConflict)
	require.Equal(t, 1, r.writes)
}

func TestModelModifierSettingsNodeRefreshFailsClosed(t *testing.T) {
	s, r := modelModifierServiceFixture(t, "startup@effort:high")
	ctx := context.Background()
	require.NoError(t, s.EnsureModelModifierPolicy(ctx))
	raw := `{"use_startup":false,"exemptions":["other@effort:high"]}`
	r.mu.Lock()
	r.raw = &raw
	r.mu.Unlock()
	expireModelModifierCache(s)
	require.NoError(t, s.EnsureModelModifierPolicy(ctx))
	require.Equal(t, "other@effort:high", modelmodifier.Base("other@effort:high"))
	require.Equal(t, "startup", modelmodifier.Base("startup@effort:high"))
	for _, invalid := range []string{`null`, `{}`, `{"use_startup":false,"exemptions":null}`, `{"exemptions":["re:["]}`, strings.Repeat("x", (512<<10)+1)} {
		r.mu.Lock()
		r.raw = &invalid
		r.mu.Unlock()
		expireModelModifierCache(s)
		require.Error(t, s.EnsureModelModifierPolicy(ctx), invalid[:min(len(invalid), 80)])
		require.Equal(t, "other@effort:high", modelmodifier.Base("other@effort:high"), "failed refresh must preserve the last valid policy while rejecting admission")
	}
	r.mu.Lock()
	r.err = errors.New("database offline")
	r.mu.Unlock()
	expireModelModifierCache(s)
	require.Error(t, s.EnsureModelModifierPolicy(ctx))
	require.Equal(t, "other@effort:high", modelmodifier.Base("other@effort:high"))
}

func TestModelModifierSettingsSaveSupersedesInFlightRead(t *testing.T) {
	s, r := modelModifierServiceFixture(t, "old@effort:high")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	initial, err := s.ModelModifierSettings(ctx)
	require.NoError(t, err)
	started, release := make(chan struct{}), make(chan struct{})
	r.onRead = func(ctx context.Context, n int) error {
		if n == 2 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	readResult := make(chan ModelModifierSettings, 1)
	readErr := make(chan error, 1)
	go func() { value, err := s.ModelModifierSettings(ctx); readResult <- value; readErr <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	initial.UseStartup, initial.Exemptions = false, []string{"new@effort:high"}
	saved, err := s.SaveModelModifierSettings(ctx, initial)
	close(release)
	require.NoError(t, err)
	require.Equal(t, []string{"new@effort:high"}, saved.Exemptions)
	select {
	case value := <-readResult:
		require.NoError(t, <-readErr)
		require.Equal(t, saved, value, "older reads must join the authoritative refresh, not return stale or zero settings")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Equal(t, "new@effort:high", modelmodifier.Base("new@effort:high"))
}

func TestModelModifierSettingsCancelledWaiterDoesNotCancelSharedRead(t *testing.T) {
	s, r := modelModifierServiceFixture(t, "real@effort:high")
	started, release := make(chan struct{}), make(chan struct{})
	r.onRead = func(ctx context.Context, _ int) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- s.EnsureModelModifierPolicy(ctx) }()
	<-started
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	close(release)
	value, err := s.loadModelModifierSettings(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, []string{"real@effort:high"}, value.Exemptions)
	r.mu.Lock()
	require.Equal(t, 1, r.reads)
	r.mu.Unlock()
}
