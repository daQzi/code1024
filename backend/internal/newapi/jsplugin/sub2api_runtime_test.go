package jsplugin

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/builtin"
	"github.com/stretchr/testify/require"
)

func TestSub2APIBuiltinContractBaseline(t *testing.T) {
	keys := builtin.Keys()
	require.Len(t, keys, 19) // 17 original plugins and 2 code1024 channel adapters.
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			source, err := builtin.Source(key)
			require.NoError(t, err)
			plugin, err := CompilePlugin(source, Options{Timeout: time.Second})
			require.NoError(t, err)
			require.Equal(t, key, plugin.Meta.Key)
			require.True(t, plugin.Meta.HasTaskPart() || plugin.Meta.HasProviderPart() || plugin.Meta.HasTransformPart())
		})
	}
}

func TestSub2APIHookCancellationAndReuse(t *testing.T) {
	engine, err := Compile(`export function spin() { while(true) {} } export function ok() { return 42; }`, Options{Timeout: 30 * time.Millisecond, Concurrency: 1})
	require.NoError(t, err)
	_, err = engine.Call(context.Background(), "spin")
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = engine.Call(ctx, "ok")
	require.ErrorIs(t, err, context.Canceled)
	value, err := engine.Call(context.Background(), "ok")
	require.NoError(t, err)
	require.EqualValues(t, 42, value)
}
