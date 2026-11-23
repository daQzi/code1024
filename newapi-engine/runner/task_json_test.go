package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
)

const orderedTaskSource = `
export const meta = {apiVersion:1,key:"ordered-task",name:"Ordered",version:"1.0.0",author:{name:"Test"},models:["video"],protocols:["openai_video"],fetchMode:"per_task",CAPABILITIES};
function decode(ctx) { return {kind:"submit",model:"video",action:"text_to_video",requestBody:ctx.body.value}; }
export const protocols = {openai_video:{decodeRequest:decode, render(ctx,task){return task;}}};
export const native = {decode};
function build(ctx) { const body = {z:"<>&",inputKeys:Object.keys(ctx.requestBody).join(","),a:{...ctx.requestBody}}; ctx.requestBody.mutated=true; return {url:ctx.baseUrl+"/task",body}; }
export function buildSubmitRequest(ctx) {return ctx.stringify ? stringifyBody(ctx) : build(ctx);}
export function buildQueryRequest(ctx) {return build(ctx);}
export function buildContentRequest(ctx) {return build(ctx);}
export function listArtifacts(){return [{key:"video",type:"video"}];}
export function parseSubmitResponse(ctx,resp) {return {taskId:resp.body.id};}
export function parseTaskResult(ctx,body) {return body;}
export function stringifyBody(ctx) {return {url:ctx.baseUrl+"/task",body:{toJSON(){return {z:"<>&",a:1,omitted:undefined};}}};}
`

func orderedTaskClient(t *testing.T, ordered bool, suffix string) *Client {
	t.Helper()
	capabilities := ""
	if ordered {
		capabilities = `requiredCapabilities:["json-order@1"]`
	}
	source := strings.Replace(orderedTaskSource, "CAPABILITIES", capabilities, 1) + suffix
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Start(ctx, executable, []string{"-test.run=^TestRunnerWorkerHelper$", "--", "plugin-test-worker"}, source)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func TestRunnerTaskOrderedBodiesAcrossDecodersAndDriverCalls(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		name := "default"
		if ordered {
			name = "ordered"
		}
		t.Run(name, func(t *testing.T) {
			client := orderedTaskClient(t, ordered, "")
			original := json.RawMessage(`{"z":{"y":1,"b":2},"a":"<>&","10":10,"2":2}`)
			wantKeys := "2,10,a,z"
			wantBody := `{"10":10,"2":2,"a":"\u003c\u003e\u0026","z":{"b":2,"y":1}}`
			if ordered {
				wantKeys = "2,10,z,a"
				wantBody = `{"2":2,"10":10,"z":{"y":1,"b":2},"a":"<>&"}`
			}
			for _, decoder := range []struct {
				hook string
				path []string
			}{{"protocols", []string{"openai_video", "decodeRequest"}}, {"native", []string{"decode"}}} {
				raw, err := client.Call(context.Background(), decoder.hook, decoder.path, map[string]any{"body": map[string]any{"kind": "json", "value": original}})
				require.NoError(t, err)
				var decoded struct{ RequestBody json.RawMessage }
				require.NoError(t, json.Unmarshal(raw, &decoded))
				require.Equal(t, wantBody, string(decoded.RequestBody))
				for _, hook := range []string{"buildSubmitRequest", "buildQueryRequest", "buildContentRequest", "buildSubmitRequest"} {
					raw, err = client.Call(context.Background(), hook, nil, map[string]any{"baseUrl": "https://example.com", "requestBody": decoded.RequestBody})
					require.NoError(t, err)
					var result struct{ Body json.RawMessage }
					require.NoError(t, json.Unmarshal(raw, &result))
					if ordered {
						require.Equal(t, `{"z":"<>&","inputKeys":"`+wantKeys+`","a":`+wantBody+`}`, string(result.Body), "JS index keys follow the language's numeric ordering")
					} else {
						require.JSONEq(t, `{"z":"<>&","inputKeys":"`+wantKeys+`","a":`+wantBody+`}`, string(result.Body))
					}
				}
			}
			require.Equal(t, `{"z":{"y":1,"b":2},"a":"<>&","10":10,"2":2}`, string(original))
		})
	}
}

func TestRunnerTaskOrderedDescriptorUsesJSONStringify(t *testing.T) {
	client := orderedTaskClient(t, true, "")
	raw, err := client.Call(context.Background(), "buildSubmitRequest", nil, map[string]any{"baseUrl": "https://example.com", "stringify": true})
	require.NoError(t, err)
	var result struct{ Body json.RawMessage }
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, `{"z":"<>&","a":1}`, string(result.Body), "descriptor toJSON must run and undefined members must be omitted")
	raw, err = client.Call(context.Background(), "stringifyBody", nil, map[string]any{"baseUrl": "https://example.com"})
	require.NoError(t, err)
	plugin, err := jsplugin.CompilePlugin(strings.Replace(orderedTaskSource, "CAPABILITIES", `requiredCapabilities:["json-order@1"]`, 1), jsplugin.Options{Concurrency: 1, HostCapability: SupportsCapability})
	require.NoError(t, err)
	var baseline json.RawMessage
	require.NoError(t, plugin.Engine.CallInto(context.Background(), &baseline, "stringifyBody", map[string]any{"baseUrl": "https://example.com"}))
	require.Equal(t, string(baseline), string(raw), "arbitrary hooks retain the original engine codec")
}

func TestRunnerTaskBodyBoundsAndPrecision(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		raw, err := taskHookArgument(json.RawMessage(`{"requestBody":{"z":9007199254740993,"a":1},"state":{"z":2,"a":1}}`), ordered)
		require.NoError(t, err)
		encoded := raw.(jsplugin.RawJSON)
		require.Contains(t, string(encoded), "9007199254740993", "normalizing RPC must not introduce float64 rounding")
		require.Contains(t, string(encoded), `"state":{"z":2,"a":1}`, "state is not the opt-in request body")
		client := orderedTaskClient(t, ordered, "")
		_, err = client.Call(context.Background(), "buildSubmitRequest", nil, map[string]any{"requestBody": json.RawMessage(`{"prompt":"` + strings.Repeat("x", jsplugin.MaxJSONToolBytes) + `"}`)})
		require.ErrorContains(t, err, "body exceeds limit")
		_, err = client.Call(context.Background(), "buildSubmitRequest", nil, map[string]any{"baseUrl": "https://example.com", "requestBody": json.RawMessage(`{"ok":true}`)})
		require.NoError(t, err, "bad input must not execute or poison the worker")
	}
}

func TestRunnerOrderedInputDoesNotExpandHTMLAcrossRPC(t *testing.T) {
	client := orderedTaskClient(t, true, "")
	body := json.RawMessage(`{"z":"` + strings.Repeat("<>&", 300000) + `","a":1}`)
	raw, err := client.Call(context.Background(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"body": map[string]any{"kind": "json", "value": body}})
	require.NoError(t, err, "a valid sub-1-MiB body must not exceed limits because the RPC HTML-escaped it")
	var result struct{ RequestBody json.RawMessage }
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, string(body), string(result.RequestBody))
}

func BenchmarkTaskHookBodyNormalization(b *testing.B) {
	for _, size := range []int{64 << 10, 900 << 10} {
		for _, ordered := range []bool{false, true} {
			b.Run(fmt.Sprintf("bytes=%d/ordered=%t", size, ordered), func(b *testing.B) {
				raw := json.RawMessage(`{"requestBody":{"z":"` + strings.Repeat("x", size) + `","a":1},"state":{}}`)
				b.ReportAllocs()
				b.SetBytes(int64(len(raw)))
				for b.Loop() {
					if _, err := taskHookArgument(raw, ordered); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
