package runner

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
)

func TestPoolAdmissionCancellationAndDrain(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	p := NewPool(executable, []string{"-test.run=^TestRunnerWorkerHelper$", "--", "plugin-test-worker"})
	defer p.Close()
	entered := make(chan struct{}, MaxProcesses)
	release := make(chan struct{})
	finished := make(chan error, MaxProcesses)
	for range MaxProcesses {
		go func() {
			finished <- p.With(context.Background(), testSource, func(*Client) error { entered <- struct{}{}; <-release; return nil })
		}()
	}
	for range MaxProcesses {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err = p.With(ctx, testSource, func(*Client) error { t.Fatal("must not execute while pool is full"); return nil })
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	for range MaxProcesses {
		require.NoError(t, <-finished)
	}
	p.Close()
	err = p.With(context.Background(), testSource, func(*Client) error { return nil })
	require.ErrorIs(t, err, ErrClosed)
}

func TestRunnerMetadataConfigAndTypedValidation(t *testing.T) {
	client := startTestClient(t)
	require.True(t, client.Descriptor.Task)
	require.False(t, client.Descriptor.Transform)
	config, err := client.ValidateConfig(context.Background(), json.RawMessage(`{"unknown":"discard"}`), false)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(config))
	_, err = client.Typed(context.Background(), nil, "CompilePlugin")
	require.Error(t, err)
}

func TestRunnerFetchCallbackIsScopedToCall(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	source := testSource + `export function fetching(){ return utils.fetch({url:"https://example.com/test"}); }`
	client, err := Start(context.Background(), executable, []string{"-test.run=^TestRunnerWorkerHelper$", "--", "plugin-test-worker"}, source)
	require.NoError(t, err)
	defer client.Close()
	called := 0
	data, err := client.CallWithFetcher(context.Background(), func(ctx context.Context, req jsplugin.FetchRequest) (jsplugin.FetchResponse, error) {
		called++
		require.Equal(t, "https://example.com/test", req.URL)
		return jsplugin.FetchResponse{Status: 200, Body: []byte(`{"fixture":true}`)}, nil
	}, "fetching", nil)
	require.NoError(t, err)
	require.NotEmpty(t, data)
	require.Equal(t, 1, called)
	_, err = client.Call(context.Background(), "fetching", nil)
	require.Error(t, err)
}

func TestPoolFreshCallsDoNotObserveOrRetainLiveGlobals(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	p := NewPool(executable, []string{"-test.run=^TestRunnerWorkerHelper$", "--", "plugin-test-worker"})
	defer p.Close()
	source := testSource + `let privateValue=0; export function probe(){return ++privateValue;}`
	for range MaxProcesses {
		require.NoError(t, p.With(context.Background(), source, func(c *Client) error {
			raw, err := c.Call(context.Background(), "probe", nil)
			require.NoError(t, err)
			require.JSONEq(t, `1`, string(raw))
			return nil
		}))
	}
	for range MaxProcesses * 2 {
		require.NoError(t, p.WithFresh(context.Background(), source, func(c *Client) error {
			raw, err := c.Call(context.Background(), "probe", nil)
			require.NoError(t, err)
			require.JSONEq(t, `1`, string(raw), "offline calls cannot observe live globals or earlier fixture calls")
			return nil
		}))
	}
}
