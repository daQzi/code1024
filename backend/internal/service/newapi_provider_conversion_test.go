package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const providerConverterHooks = `
export function convertRequest(ctx, req) {
  return {body:{deployment:req.model,text:req.body.input || "hello",wants_stream:req.stream},
    path:"/converted/start",headers:{"X-Converted":"yes"},
    state:{model:req.model,path:ctx.path,label:ctx.config.label,count:0}};
}
export function prepareRequest(ctx, req) {
  if(req.model!=="vendor-text" || req.upstreamPath!=="/prefix/converted/start" ||
     req.headers["X-Vendor-Key"] || req.headers.Authorization || req.body.model ||
     req.body.deployment!==req.model || req.stream!==req.body.wants_stream) throw Error("bad preparation context");
  return {path:"/prepared/send?mode=custom",effects:[
    {type:"json.patch",ops:[{op:"add",path:"/prepared",value:true}]},
    {type:"header.patch",set:{"X-Prepared":ctx.config.label}}]};
}
export function convertResponse(ctx, resp) {
  if(resp.status>=400) return {error:{status:resp.status,message:"Vendor rejected request",type:"vendor_error",code:"denied"}};
  const body=JSON.parse(resp.body), state=ctx.state;
  if(state.label!=="saved" || ctx.config.label!=="saved") throw Error("configuration drifted");
  if(state.path==="/v1/responses") return {body:{id:"resp-converted",object:"response",status:"completed",error:null,model:state.model,
    output:[{type:"message",role:"assistant",content:[{type:"output_text",text:body.reply}]}],
    usage:{input_tokens:body.input,output_tokens:body.output,total_tokens:body.input+body.output}}};
  return {body:{id:"chat-converted",object:"chat.completion",model:state.model,
    choices:[{index:0,message:{role:"assistant",content:body.reply},finish_reason:"stop"}],
    usage:{prompt_tokens:body.input,completion_tokens:body.output,total_tokens:body.input+body.output}}};
}
export function convertStreamEvent(ctx, event) {
  const state=ctx.state;
  if(event.done) return {events:state.finished && state.path==="/v1/chat/completions" ? [{data:"[DONE]"}] : []};
  const value=JSON.parse(event.data);
  if(value.fail) return {error:{message:"Vendor stream failed",status:502}};
  if(value.seq!==state.count || state.label!=="saved") throw Error("state drifted");
  state.count++;
  if(value.usage) return {state,events:[{data:state.path==="/v1/responses" ?
    {type:"response.in_progress",response:{id:"resp-converted",status:"in_progress",model:state.model,usage:{input_tokens:value.input,output_tokens:value.output}}} :
    {model:state.model,choices:[],usage:{prompt_tokens:value.input,completion_tokens:value.output}}}]};
  if(value.finished) {
    state.finished=true;
    const data=state.path==="/v1/responses" ?
      {type:"response.completed",response:{id:"resp-converted",object:"response",model:state.model,status:"completed",error:null,output:[],usage:{input_tokens:value.input,output_tokens:value.output}}} :
      {model:state.model,choices:[{index:0,delta:{},finish_reason:"stop"}],usage:{prompt_tokens:value.input,completion_tokens:value.output}};
    return {state,events:[{data}]};
  }
  return {state,events:[{data:state.path==="/v1/responses" ? {type:"response.output_text.delta",delta:value.text} :
    {model:state.model,choices:[{index:0,delta:{content:value.text},finish_reason:null}]}}]};
}`

func providerConverterSource() string {
	return strings.ReplaceAll(providerFixtureSource, `upstream_path:`, `converter:"plugin",upstream_path:`) + providerConverterHooks
}

func TestNewAPIProviderPreparation(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		source := providerFixtureSource + `export function prepareRequest(ctx,req){
      if(req.headers.Authorization || req.headers["X-Vendor-Key"] || req.model!=="vendor-text") throw Error("credentials exposed");
      return {path:"/regional/{model}?region=one",effects:[
        {type:"json.patch",ops:[{op:"add",path:"/temperature",value:0.2}]},
        {type:"header.patch",set:{"X-Prepared":ctx.config.label}}]};}`
		s, account, _ := providerFixture(t, source)
		exchange, err := s.buildProviderRequest(context.Background(), account, path, "vendor-text", []byte(`{"model":"vendor-text","input":"hello"}`))
		require.NoError(t, err)
		req := exchange.request
		assert.Equal(t, "/prefix/regional/vendor-text", req.URL.Path)
		assert.Equal(t, "one", req.URL.Query().Get("region"))
		if path == newAPIProviderResponses {
			assert.Equal(t, "upstream-fixture-key", req.URL.Query().Get("key"), "query auth survives target override")
		} else {
			assert.Equal(t, "upstream-fixture-key", req.Header.Get("X-Vendor-Key"))
		}
		sent, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{"model":"vendor-text","input":"hello","temperature":0.2}`, string(sent))
		assert.Equal(t, int64(len(sent)), req.ContentLength)
		digest := sha256.Sum256(sent)
		assert.Equal(t, hex.EncodeToString(digest[:]), req.Header.Get("X-Body-Digest"))
		assert.Equal(t, "saved", req.Header.Get("X-Prepared"))
	}
}

func TestNewAPIProviderPreparationGuards(t *testing.T) {
	for _, effect := range []string{
		`{type:"header.patch",set:{"Authorization":"changed"}}`,
		`{type:"header.patch",remove:["X-Vendor-Key"]}`,
		`{type:"header.patch",set:{"Content-Length":"0"}}`,
		`{type:"json.patch",ops:[{op:"replace",path:"/model",value:"other"}]}`,
		`{type:"json.patch",ops:[{op:"add",path:"/Model",value:"other"}]}`,
		`{type:"json.patch",ops:[{op:"add",path:"/stream",value:true}]}`,
		`{type:"json.patch",ops:[{op:"add",path:"/stream_options",value:{include_usage:false}}]}`,
		`{type:"json.patch",ops:[{op:"add",path:"/n",value:2}]}`,
		`{type:"json.patch",ops:[{op:"add",path:"/N",value:2}]}`,
		`{type:"json.patch",ops:[{op:"add",path:"/generationConfig",value:{candidateCount:2}}]}`,
		`{type:"json.patch",ops:[{op:"add",path:"/generationConfig",value:{CandidateCount:2}}]}`,
		`{type:"json.patch",ops:[{op:"add",path:"/GenerationConfig",value:{candidateCount:2}}]}`,
		`{type:"json.patch",ops:[{op:"add",path:"/service_tier",value:"priority"}]}`,
		`{type:"json.patch",ops:[{op:"add",path:"/duration",value:10}]}`,
		`{type:"json.patch",ops:[{op:"test",path:"/model",value:"wrong"}]}`,
		`{type:"json.patch",ops:[{op:"add",path:"/padding",value:"x".repeat(1100000)}]}`,
	} {
		t.Run(effect[:min(90, len(effect))], func(t *testing.T) {
			source := providerFixtureSource + `export function prepareRequest(){return {effects:[{type:"header.patch",set:{"X-Atomic":"yes"}},` + effect + `]}}`
			s, account, upstream := providerFixture(t, source)
			body := []byte(`{"model":"public-text","messages":[]}`)
			c, recorder := providerContext(newAPIProviderChat, string(body))
			_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, body, newAPIProviderChat, "")
			require.Error(t, err)
			assert.Empty(t, upstream.requests, "invalid effects must be atomic and never send")
			assert.NotContains(t, recorder.Body.String(), "upstream-fixture-key")
		})
	}
	for _, code := range []string{`return {url:"https://unapproved.example/send"}`, `utils.fetch("https://api.example.com"); return null`, `throw Error("upstream-fixture-key")`} {
		s, account, upstream := providerFixture(t, providerFixtureSource+`export function prepareRequest(){`+code+`}`)
		body := []byte(`{"model":"public-text","messages":[]}`)
		c, recorder := providerContext(newAPIProviderChat, string(body))
		_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, body, newAPIProviderChat, "")
		require.Error(t, err)
		assert.Empty(t, upstream.requests)
		assert.NotContains(t, recorder.Body.String(), "upstream-fixture-key")
	}
}

func TestNewAPIProviderCustomConversion(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, kind := range []string{"application/json", "text/event-stream", "application/x-ndjson"} {
			t.Run(path+"/"+kind, func(t *testing.T) {
				s, account, upstream := providerFixture(t, providerConverterSource())
				stream := kind != "application/json"
				body := `{"model":"public-text","messages":[],"input":"hello","stream":` + map[bool]string{true: "true", false: "false"}[stream] + `}`
				upstream.response, upstream.contentType = `{"reply":"hello","input":12,"output":5}`, kind
				if stream {
					upstream.response = "{\"seq\":0,\"text\":\"hello\"}\n{\"seq\":1,\"finished\":true,\"input\":12,\"output\":5}\n"
					if kind == "text/event-stream" {
						upstream.response = ": heartbeat\r\n\r\nevent: token\r\ndata: {\"seq\":0,\r\ndata: \"text\":\"hello\"}\r\n\r\ndata: {\"seq\":1,\"finished\":true,\"input\":12,\"output\":5}\r\n\r\n"
					}
				}
				c, recorder := providerContext(path, body)
				result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), path, "")
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, 12, result.Usage.InputTokens)
				assert.Equal(t, 5, result.Usage.OutputTokens)
				assert.Equal(t, "vendor-text", result.BillingModel)
				assert.Equal(t, "public-text", result.Model)
				assert.Equal(t, stream, result.Stream)
				assertNewAPIProviderExistingBilling(t, result, account)
				assert.Contains(t, recorder.Body.String(), "public-text")
				if path == newAPIProviderChat || !stream {
					assert.Contains(t, recorder.Body.String(), "hello")
				}
				require.Len(t, upstream.requests, 1)
				req := upstream.requests[0]
				assert.Equal(t, "/prefix/prepared/send", req.URL.Path)
				assert.Equal(t, req.URL.Path, result.UpstreamEndpoint)
				assert.Equal(t, "yes", req.Header.Get("X-Converted"))
				assert.Equal(t, "saved", req.Header.Get("X-Prepared"))
				sent, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				digest := sha256.Sum256(sent)
				assert.Equal(t, hex.EncodeToString(digest[:]), req.Header.Get("X-Body-Digest"))
				assert.JSONEq(t, `{"deployment":"vendor-text","text":"hello","wants_stream":`+map[bool]string{true: "true", false: "false"}[stream]+`,"prepared":true}`, string(sent))
			})
		}
	}
}

func TestNewAPIProviderConvertedFailures(t *testing.T) {
	for _, tc := range []struct {
		name, replace, with, response, kind string
		stream                              bool
		status                              int
	}{
		{name: "vendor-http-error", response: `{"failure":true}`, status: 400},
		{name: "http-error-cannot-be-success", replace: `if(resp.status>=400) return {error:{status:resp.status,message:"Vendor rejected request",type:"vendor_error",code:"denied"}};`, with: "", response: `{"reply":"hello","input":12,"output":5}`, status: 400},
		{name: "failed-body", replace: `const body=JSON.parse(resp.body), state=ctx.state;`, with: `return {error:{message:"Vendor failed"}};`, response: `{}`},
		{name: "chained-request", replace: `const body=JSON.parse(resp.body), state=ctx.state;`, with: `return {next:{path:"/again",body:{}},state:{}};`, response: `{}`},
		{name: "tool-billing", replace: `const body=JSON.parse(resp.body), state=ctx.state;`, with: `return {body:{},tools:{search:1}};`, response: `{}`},
		{name: "response-fetch", replace: `const body=JSON.parse(resp.body), state=ctx.state;`, with: `utils.fetch("https://api.example.com"); return {body:{}};`, response: `{}`},
		{name: "invalid-json-output", replace: `const body=JSON.parse(resp.body), state=ctx.state;`, with: `return {body:"not JSON"};`, response: `{}`},
		{name: "failed-converted-body", replace: `const body=JSON.parse(resp.body), state=ctx.state;`, with: `return {body:{error:{message:"failed"},choices:[],usage:{prompt_tokens:12}}};`, response: `{}`},
		{name: "non-json-output", replace: `const body=JSON.parse(resp.body), state=ctx.state;`, with: `return {body:"hello",contentType:"text/plain"};`, response: `{}`},
		{name: "oversize-response", response: strings.Repeat("x", MaxRequestPluginBody+1)},
		{name: "stream-mode-change", replace: `path:"/converted/start"`, with: `stream:false,path:"/converted/start"`, stream: true},
		{name: "stream-missing-terminal", response: "{\"seq\":0,\"text\":\"partial\"}\n", kind: "application/x-ndjson", stream: true},
		{name: "stream-vendor-error", response: "{\"seq\":0,\"fail\":true}\n", kind: "application/x-ndjson", stream: true},
		{name: "stream-error-after-usage", response: "{\"seq\":0,\"usage\":true,\"input\":12,\"output\":5}\n{\"seq\":1,\"fail\":true}\n", kind: "application/x-ndjson", stream: true},
		{name: "stream-wrong-kind", response: `{}`, kind: "application/json", stream: true},
		{name: "json-received-stream", response: "{}\n", kind: "text/event-stream"},
		{name: "stream-body-output", replace: `const state=ctx.state;`, with: `return {body:{}};`, response: "{}\n", kind: "application/x-ndjson", stream: true},
		{name: "stream-oversize-event", response: strings.Repeat("x", MaxRequestPluginBody+1), kind: "text/event-stream", stream: true},
		{name: "stream-malformed-event", response: "not JSON\n", kind: "application/x-ndjson", stream: true},
	} {
		for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
			t.Run(tc.name+path, func(t *testing.T) {
				source := providerConverterSource()
				if tc.replace != "" {
					source = strings.ReplaceAll(source, tc.replace, tc.with)
				}
				s, account, upstream := providerFixture(t, source)
				upstream.response, upstream.contentType, upstream.status = tc.response, tc.kind, tc.status
				body := `{"model":"public-text","messages":[],"input":"hello","stream":` + map[bool]string{true: "true", false: "false"}[tc.stream] + `}`
				c, recorder := providerContext(path, body)
				_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), path, "")
				require.Error(t, err)
				maxRequests := 1
				if tc.name == "chained-request" {
					maxRequests += newAPIProviderMaxFollowups
				}
				assert.LessOrEqual(t, len(upstream.requests), maxRequests)
				assert.NotContains(t, recorder.Body.String(), "upstream-fixture-key")
				assert.NotContains(t, recorder.Body.String(), "[DONE]", "failure must not fabricate a clean stream end")
			})
		}
	}
}

func TestNewAPIProviderConvertedRequestGuards(t *testing.T) {
	for _, replacement := range []string{
		`return {body:{},url:"https://unapproved.example/send"};`,
		`return {body:{},headers:{"X-Vendor-Key":"override"}};`,
		`return {body:{},headers:{Authorization:"override"}};`,
		`return {body:{},headers:{Host:"evil.example"}};`,
		`return {body:{},contentType:"text/plain"};`,
		`return {form:{fields:{text:"hello"}}};`,
		`return {body:{padding:"x".repeat(1100000)}};`,
		`utils.fetch("https://api.example.com"); return {body:{}};`,
	} {
		source := strings.Replace(providerConverterSource(), "export function convertRequest(ctx, req) {", "export function convertRequest(ctx, req) {"+replacement, 1)
		s, account, upstream := providerFixture(t, source)
		body := []byte(`{"model":"public-text","messages":[]}`)
		c, _ := providerContext(newAPIProviderChat, string(body))
		_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, body, newAPIProviderChat, "")
		require.Error(t, err)
		assert.Empty(t, upstream.requests)
	}
}

func TestNewAPIProviderPinnedConversionAndReleasedWorkers(t *testing.T) {
	s, account, _ := providerFixture(t, providerConverterSource())
	exchange, err := s.buildProviderRequest(context.Background(), account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text","stream":true}`))
	require.NoError(t, err)
	// Mutating the installed version/config after request preparation cannot
	// replace this request's response converter or its configuration.
	repo := s.repo.(*requestPluginFixtureRepo)
	repo.versions[1].Source = `throw Error("new-version-must-not-run")`
	repo.versions[1].Enabled = false
	account.Credentials[newAPIPluginConfigEncryptedKey] = "changed-after-request"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var streams []*newAPIProviderStream
	for range runner.MaxProcesses + 2 {
		body := newNewAPIProviderStream(ctx, s, exchange, io.NopCloser(strings.NewReader("{\"seq\":0,\"text\":\"hello\"}\n{\"seq\":1,\"finished\":true,\"input\":12,\"output\":5}\n")), false)
		t.Cleanup(func() { _ = body.Close() })
		_, err := body.Read(make([]byte, 1))
		require.NoError(t, err, "partially consumed streams must release their workers")
		streams = append(streams, body)
	}
	for _, stream := range streams {
		output, err := io.ReadAll(stream)
		require.NoError(t, err)
		assert.Contains(t, string(output), "vendor-text")
		assert.Contains(t, string(output), "[DONE]")
	}
}

func TestNewAPIProviderStreamTransportFailureAndCancellation(t *testing.T) {
	s, account, _ := providerFixture(t, providerConverterSource())
	exchange, err := s.buildProviderRequest(context.Background(), account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text","stream":true}`))
	require.NoError(t, err)
	stream := newNewAPIProviderStream(context.Background(), s, exchange, io.NopCloser(io.MultiReader(strings.NewReader("{\"seq\":0,\"text\":\"partial\"}\n"), iotest.ErrReader(errors.New("reset")))), false)
	t.Cleanup(func() { _ = stream.Close() })
	output, err := io.ReadAll(stream)
	require.Error(t, err)
	assert.NotContains(t, string(output), "[DONE]")
	assert.NotNil(t, stream.failure)

	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer writer.Close()
	cancelled := newNewAPIProviderStream(ctx, s, exchange, reader, true)
	result := make(chan error, 1)
	go func() { _, err := io.ReadAll(cancelled); result <- err }()
	cancel()
	select {
	case err := <-result:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled upstream read did not stop")
	}
	_ = cancelled.Close()
}

func TestNewAPIProviderNativeResponse(t *testing.T) {
	source := strings.Replace(providerConverterSource(), `path:"/converted/start"`, `nativeResponse:true,path:"/converted/start"`, 1)
	s, account, upstream := providerFixture(t, source)
	upstream.response = `{"model":"vendor-text","choices":[{"message":{"content":"native"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`
	body := []byte(`{"model":"public-text","messages":[]}`)
	c, recorder := providerContext(newAPIProviderChat, string(body))
	result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, body, newAPIProviderChat, "")
	require.NoError(t, err)
	assert.Equal(t, 12, result.Usage.InputTokens)
	assert.Contains(t, recorder.Body.String(), "native")
}

func TestNewAPIProviderDocumentedTemplate(t *testing.T) {
	doc, err := os.ReadFile("../../../docs/customization/PROVIDER_PLUGINS.md")
	require.NoError(t, err)
	_, source, found := strings.Cut(string(doc), "```js\n")
	require.True(t, found)
	source, _, found = strings.Cut(source, "\n```")
	require.True(t, found)
	s, repo := requestPluginFixture(t, source)
	repo.versions[1].Enabled = true
	account := &Account{ID: 17, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		NewAPIPluginRevisionKey: 1, "base_url": "https://api.example.com", "api_key": "fixture-key",
		"model_mapping": map[string]any{"public-text": "vendor-text"},
	}}
	require.NoError(t, s.NormalizeAccountBinding(context.Background(), account, map[string]any{}))
	s.gateway.newAPIPlugins, s.gateway.cfg = s, &config.Config{}
	upstream := s.gateway.httpUpstream.(*generationFixtureHTTP)
	upstream.response = `{"id":"vendor-fixture","text":"hello","input_tokens":12,"output_tokens":5}`
	body := []byte(`{"model":"public-text","messages":[{"role":"user","content":"hello"}]}`)
	c, recorder := providerContext(newAPIProviderChat, string(body))
	result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, body, newAPIProviderChat, "")
	require.NoError(t, err)
	assert.Contains(t, recorder.Body.String(), "hello")
	assert.Equal(t, "/generate", result.UpstreamEndpoint)
	assert.Equal(t, "fixture-key", upstream.requests[0].Header.Get("X-API-Key"))
	assert.Equal(t, "sub2api", upstream.requests[0].Header.Get("X-Client"))
	assertNewAPIProviderExistingBilling(t, result, account)
}

func TestNewAPIProviderStreamIdleTimeout(t *testing.T) {
	s, account, _ := providerFixture(t, providerConverterSource())
	exchange, err := s.buildProviderRequest(context.Background(), account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text","stream":true}`))
	require.NoError(t, err)
	reader, writer := io.Pipe()
	defer writer.Close()
	stream := newNewAPIProviderStream(context.Background(), s, exchange, reader, true)
	t.Cleanup(func() { _ = stream.Close() })
	stream.idleTimeout = time.Millisecond
	result := make(chan error, 1)
	go func() { _, err := io.ReadAll(stream); result <- err }()
	select {
	case err := <-result:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("idle upstream read did not stop")
	}
}

func TestNewAPIProviderFailedErrorConversionPreservesHTTPStatus(t *testing.T) {
	source := strings.Replace(providerConverterSource(), `export function convertResponse(ctx, resp) {`, `export function convertResponse(ctx, resp) {throw Error("private-plugin-diagnostic");`, 1)
	s, account, _ := providerFixture(t, source)
	exchange, err := s.buildProviderRequest(context.Background(), account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text"}`))
	require.NoError(t, err)
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		body := `{"error":{"message":"upstream rejected request"}}`
		resp := &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
		require.NoError(t, s.convertProviderResponse(context.Background(), exchange, resp, status == http.StatusUnauthorized))
		assert.Equal(t, status, resp.StatusCode)
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, body, string(got))
		_ = resp.Body.Close()
	}
}

func TestNewAPIProviderPreparationPreservesVendorJSON(t *testing.T) {
	for _, tc := range []struct{ body, patch string }{
		{`{Model:req.model,Stream:req.stream,generationConfig:{candidateCount:1}}`, `"/business"`},
		{`[{text:"hello"}]`, `"/0/business"`},
	} {
		source := strings.ReplaceAll(providerFixtureSource, `upstream_path:`, `converter:"plugin",upstream_path:`) + `
export function convertRequest(ctx,req){return {body:` + tc.body + `}}
export function prepareRequest(){return {effects:[{type:"json.patch",ops:[{op:"add",path:` + tc.patch + `,value:"kept"}]}]}}
export function convertResponse(){return {body:{}}}
export function convertStreamEvent(){return {events:[]}}`
		s, account, _ := providerFixture(t, source)
		exchange, err := s.buildProviderRequest(context.Background(), account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text","stream":false}`))
		require.NoError(t, err, "a vendor schema need not use OpenAI field casing or an object root")
		body, err := io.ReadAll(exchange.request.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"business":"kept"`)
	}
}
