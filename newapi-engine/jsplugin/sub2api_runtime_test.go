package jsplugin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/stretchr/testify/require"
)

func TestSub2APIBuiltinContractBaseline(t *testing.T) {
	keys := builtin.Keys()
	require.Len(t, keys, 21) // 17 original plugins and 4 code1024 adapters.
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			source, err := builtin.Source(key)
			require.NoError(t, err)
			plugin, err := CompilePlugin(source, Options{Timeout: time.Second})
			require.NoError(t, err)
			require.Equal(t, key, plugin.Meta.Key)
			require.True(t, plugin.Meta.HasTaskPart() || plugin.Meta.HasProviderPart() || plugin.Meta.HasTransformPart())
			encoded, err := json.Marshal(plugin.Meta)
			require.NoError(t, err)
			var restored Meta
			require.NoError(t, json.Unmarshal(encoded, &restored), "compiled metadata must survive worker/database transport")
			require.JSONEq(t, string(encoded), func() string { raw, err := json.Marshal(restored); require.NoError(t, err); return string(raw) }())
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
