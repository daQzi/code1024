package runner

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testSource = `
export const meta = {apiVersion:1,key:"runner-test",name:"Runner",version:"1.0.0",author:{name:"Test"},models:["video"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx) { return {url:ctx.baseUrl+"/submit",body:ctx}; }
export function parseSubmitResponse(ctx,resp) { return {taskId:resp.body.id}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult(ctx,body) { return body; }
export function spin() { while(true) {} }
export function capabilities() { return {ordered: utils.hasCapability("json-order@1"), delta: utils.hasCapability("submit-sse-delta@1"), clone: utils.hasCapability("json-clone@1")}; }
`

func TestRunnerWorkerHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "plugin-test-worker" {
		return
	}
	if Serve(context.Background(), os.Stdin, os.Stdout) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func startTestClient(t *testing.T) *Client {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Start(ctx, executable, []string{"-test.run=^TestRunnerWorkerHelper$", "--", "plugin-test-worker"}, testSource)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func TestRunnerRoundTripAndShutdown(t *testing.T) {
	client := startTestClient(t)
	require.Equal(t, "runner-test", client.Meta.Key)
	data, err := client.Call(context.Background(), "buildSubmitRequest", nil, map[string]any{"baseUrl": "https://example.com", "upstreamModel": "video"})
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	require.Equal(t, "https://example.com/submit", result["url"])
	data, err = client.Call(context.Background(), "capabilities", nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"ordered":true,"delta":true,"clone":true}`, string(data), "optional feature probes must reflect actual host support")
	client.Close()
	_, err = client.Call(context.Background(), "buildQueryRequest", nil)
	require.ErrorIs(t, err, ErrClosed)
}

func TestRunnerCancellationTerminatesChild(t *testing.T) {
	client := startTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := client.Call(ctx, "spin", nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case <-client.waited:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled runner was not reaped")
	}
	_, err = client.Call(context.Background(), "buildQueryRequest", nil)
	require.ErrorIs(t, err, ErrClosed)
}

func TestWorkerRejectsOversizedAndMalformedFrames(t *testing.T) {
	require.Error(t, Serve(context.Background(), strings.NewReader(strings.Repeat("x", MaxFrameBytes+1)), os.Stderr))
	require.Error(t, Serve(context.Background(), strings.NewReader("not json\n"), os.Stderr))
}
