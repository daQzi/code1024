//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

func providerCollectSource(chunks bool) string {
	source := strings.ReplaceAll(providerFixtureSource, `upstream_path:`, `converter:"plugin",upstream_path:`) + `
export function convertRequest(ctx,req){return {stream:true,body:{deployment:req.model,stream:true},state:{path:ctx.path,model:req.model,clientStream:req.stream,text:""}};}
export function convertResponse(ctx){throw Error("stream must use convertStreamEvent");}
function response(s){if(s.path==="/v1/responses")return {id:"resp-collected",model:s.model,status:"completed",output:[{type:"message",role:"assistant",content:[{type:"output_text",text:s.text}]}],usage:{input_tokens:12,output_tokens:5}};
return {id:"chat-collected",model:s.model,choices:[{index:0,message:{role:"assistant",content:s.text},finish_reason:"stop"}],usage:{prompt_tokens:12,completion_tokens:5}};}
export function convertStreamEvent(ctx,e){const s=ctx.state;if(s.clientStream!==false || ctx.config.label!=="saved")throw Error("context changed");
if(e.done){if(!s.complete)return {};return FINAL;}
const v=JSON.parse(e.data);if(v.fail)return {error:{status:v.status||502,message:"upstream-fixture-key"}};
if(v.text)s.text=v.text;
if(v.finish){s.complete=true;return FIRST;}
return {state:s};}
`
	if chunks {
		source = strings.Replace(source, "FIRST", `{state:s,contentType:"application/json",chunks:[{bodyBase64:utils.base64(JSON.stringify(response(s)).slice(0,15))}]}`, 1)
		return strings.Replace(source, "FINAL", `{contentType:"application/json",chunks:[{bodyBase64:utils.base64(JSON.stringify(response(s)).slice(15))}]}`, 1)
	}
	source = strings.Replace(source, "FIRST", `{state:s}`, 1)
	return strings.Replace(source, "FINAL", `{body:response(s)}`, 1)
}

func TestNewAPIProviderCollectStreamJSON(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, kind := range []string{"text/event-stream", "application/x-ndjson"} {
			for _, chunks := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/chunks=%t", path, kind, chunks), func(t *testing.T) {
					s, account, upstream := providerFixture(t, providerCollectSource(chunks))
					upstream.contentType = kind
					upstream.response = "{\"text\":\"你好 <>&\"}\n{\"finish\":true}\n"
					if kind == "text/event-stream" {
						upstream.response = "data: {\"text\":\"你好 <>&\"}\n\ndata: {\"finish\":true}\n\n"
					}
					body := []byte(`{"model":"public-text","messages":[],"input":"hello","stream":false}`)
					c, recorder := providerContext(path, string(body))
					result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, body, path, "")
					require.NoError(t, err)
					require.NotNil(t, result)
					require.False(t, result.Stream)
					require.Equal(t, 12, result.Usage.InputTokens)
					require.Equal(t, 5, result.Usage.OutputTokens)
					assertNewAPIProviderExistingBilling(t, result, account)
					require.Contains(t, recorder.Body.String(), "你好")
					require.Contains(t, recorder.Body.String(), "public-text")
					require.NotContains(t, recorder.Body.String(), "data:")
					require.Len(t, upstream.requests, 1)
					request := upstream.requests[0]
					require.Equal(t, "text/event-stream", request.Header.Get("Accept"))
					sent, err := io.ReadAll(request.Body)
					require.NoError(t, err)
					digest := sha256.Sum256(sent)
					require.Equal(t, hex.EncodeToString(digest[:]), request.Header.Get("X-Body-Digest"))
				})
			}
		}
	}
}

func TestNewAPIProviderCollectFollowupRenewalDoesNotRepeatGeneration(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		source := strings.Replace(providerCollectSource(false), `return {stream:true,body:`, `return {path:"/collect/create",stream:false,body:`, 1)
		source = strings.Replace(source, `throw Error("stream must use convertStreamEvent");`, `return {next:{path:"/collect/poll",stream:true},state:ctx.state};`, 1)
		s, account, _ := providerFixture(t, providerExchangeSource(source))
		var paths, tokens []string
		exchanges, polls := 0, 0
		s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
			paths, tokens = append(paths, req.URL.Path), append(tokens, req.Header.Get("X-Token"))
			switch req.URL.Path {
			case "/prefix/token":
				exchanges++
				return chainResponse(200, fmt.Sprintf(`{"token":{"value":"token-%d"},"expiresAt":0}`, exchanges)), nil
			case "/prefix/collect/create":
				return chainResponse(200, `{"id":"accepted-once"}`), nil
			case "/prefix/collect/poll":
				polls++
				payload := "{\"fail\":true,\"status\":401}\n"
				if polls > 1 {
					payload = "{\"text\":\"hello\"}\n{\"finish\":true}\n"
				}
				resp := chainResponse(200, payload)
				resp.Header.Set("Content-Type", "application/x-ndjson")
				return resp, nil
			}
			t.Fatalf("unexpected request path %s", req.URL.Path)
			return nil, nil
		}}
		body := []byte(`{"model":"public-text","messages":[],"stream":false}`)
		c, recorder := providerContext(path, string(body))
		result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, body, path, "")
		require.NoError(t, err, "request paths: %v", paths)
		require.Equal(t, []string{"/prefix/token", "/prefix/collect/create", "/prefix/collect/poll", "/prefix/token", "/prefix/collect/poll"}, paths)
		require.Equal(t, "token-1", tokens[2])
		require.Equal(t, "token-2", tokens[4])
		require.Equal(t, 5, result.Usage.OutputTokens)
		require.NotContains(t, recorder.Body.String(), "upstream-fixture-key")
	}
}

func TestNewAPIProviderCollectRejectsIncompleteAndInvalidOutput(t *testing.T) {
	for _, tc := range []struct{ name, final, first, payload string }{
		{"no-body", `{}`, `{state:s}`, "{\"finish\":true}\n"},
		{"events", `{events:[{data:"[DONE]"}]}`, `{state:s}`, "{\"finish\":true}\n"},
		{"text", `{body:"hello",contentType:"text/plain"}`, `{state:s}`, "{\"finish\":true}\n"},
		{"ambiguous-json", `{body:'{"choices":[],"choices":[]}'}`, `{state:s}`, "{\"finish\":true}\n"},
		{"invalid-protocol", `{body:{hello:"world"}}`, `{state:s}`, "{\"finish\":true}\n"},
		{"body-after-chunks", `{body:response(s)}`, `{state:s,contentType:"application/json",chunks:[{bodyBase64:"ew=="}]}`, "{\"finish\":true}\n"},
		{"oversize-body", `{body:{padding:"x".repeat(1100000)}}`, `{state:s}`, "{\"finish\":true}\n"},
		{"oversize-chunks", `{contentType:"application/json",chunks:[{bodyBase64:utils.base64("x".repeat(600000))}]}`, `{state:s,contentType:"application/json",chunks:[{bodyBase64:utils.base64("x".repeat(600000))}]}`, "{\"finish\":true}\n"},
		{"unfinished", `{}`, `{state:s}`, "{\"text\":\"partial\"}\n"},
		{"failed-final-body", `{body:{error:{message:"failure"},choices:[]}}`, `{state:s}`, "{\"finish\":true}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := providerCollectSource(false)
			source = strings.Replace(source, `return {body:response(s)};`, "return "+tc.final+";", 1)
			source = strings.Replace(source, `s.complete=true;return {state:s};`, "s.complete=true;return "+tc.first+";", 1)
			s, account, upstream := providerFixture(t, source)
			upstream.response, upstream.contentType = tc.payload, "application/x-ndjson"
			body := []byte(`{"model":"public-text","messages":[],"stream":false}`)
			c, recorder := providerContext(newAPIProviderChat, string(body))
			_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, body, newAPIProviderChat, "")
			require.Error(t, err)
			require.NotContains(t, recorder.Body.String(), "upstream-fixture-key")
		})
	}
}

func TestNewAPIProviderCollectValidatesWholeUTF8AcrossChunks(t *testing.T) {
	for _, valid := range []bool{false, true} {
		wire := []byte(`{"model":"vendor-text","choices":[{"message":{"role":"assistant","content":"你好"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`)
		cut := strings.Index(string(wire), "你") + 1 // Split inside a UTF-8 codepoint.
		if !valid {
			wire[cut] = 0xff
		}
		first := fmt.Sprintf(`{state:s,contentType:"application/json",chunks:[{bodyBase64:"%s"}]}`, base64.StdEncoding.EncodeToString(wire[:cut]))
		last := fmt.Sprintf(`{contentType:"application/json",chunks:[{bodyBase64:"%s"}]}`, base64.StdEncoding.EncodeToString(wire[cut:]))
		source := providerCollectSource(false)
		source = strings.Replace(source, `return {body:response(s)};`, "return "+last+";", 1)
		source = strings.Replace(source, `s.complete=true;return {state:s};`, "s.complete=true;return "+first+";", 1)
		s, account, upstream := providerFixture(t, source)
		upstream.response, upstream.contentType = "{\"finish\":true}\n", "application/x-ndjson"
		body := []byte(`{"model":"public-text","messages":[],"stream":false}`)
		c, recorder := providerContext(newAPIProviderChat, string(body))
		result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, body, newAPIProviderChat, "")
		if valid {
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Contains(t, recorder.Body.String(), "你好")
		} else {
			require.Error(t, err)
			require.NotContains(t, recorder.Body.String(), "你好")
		}
	}
}

func TestNewAPIProviderCollectFailureCancellationAndMemory(t *testing.T) {
	for _, mode := range []string{"transport", "cancel", "capacity", "reject"} {
		t.Run(mode, func(t *testing.T) {
			s, account, _ := providerFixture(t, providerCollectSource(false))
			capacity := int64(newAPIProviderMemory)
			if mode == "capacity" {
				capacity--
			}
			budget := pkghttputil.NewBodyMemoryBudget(capacity)
			ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
			defer release()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			var upstream io.ReadCloser
			entered := make(chan struct{})
			s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
				close(entered)
				if mode == "cancel" {
					reader, writer := io.Pipe()
					t.Cleanup(func() { writer.Close() })
					upstream = reader
				} else if mode == "transport" {
					upstream = io.NopCloser(io.MultiReader(strings.NewReader("{\"finish\":true}\n"), iotest.ErrReader(errors.New("reset"))))
				} else {
					upstream = io.NopCloser(strings.NewReader("{\"fail\":true}\n"))
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/x-ndjson"}}, Body: upstream}, nil
			}}
			body := []byte(`{"model":"public-text","messages":[],"stream":false}`)
			c, recorder := providerContext(newAPIProviderChat, string(body))
			finished := make(chan error, 1)
			go func() {
				_, err := s.gateway.forwardNewAPIProvider(ctx, c, account, body, newAPIProviderChat, "")
				finished <- err
			}()
			if mode == "cancel" {
				<-entered
				cancel()
			}
			select {
			case err := <-finished:
				require.Error(t, err)
				if mode == "capacity" {
					require.ErrorIs(t, err, pkghttputil.ErrRequestBodyMemoryBudget)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("collector did not release after failure/cancellation")
			}
			require.NotContains(t, recorder.Body.String(), "upstream-fixture-key")
			release()
			other, done := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
			defer done()
			require.NoError(t, pkghttputil.ReserveRequestBodyMemory(other, capacity))
		})
	}
}

func TestNewAPIProviderCollectLateCredentialErrorCannotReplayGeneration(t *testing.T) {
	s, account, _ := providerFixture(t, providerExchangeSource(providerCollectSource(false)))
	var paths []string
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		if req.URL.Path == "/prefix/token" {
			return chainResponse(200, `{"token":{"value":"token-1"},"expiresAt":0}`), nil
		}
		resp := chainResponse(200, "{\"text\":\"generation-already-started\"}\n{\"fail\":true,\"status\":401}\n")
		resp.Header.Set("Content-Type", "application/x-ndjson")
		return resp, nil
	}}
	body := []byte(`{"model":"public-text","messages":[],"stream":false}`)
	c, recorder := providerContext(newAPIProviderChat, string(body))
	_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, body, newAPIProviderChat, "")
	require.Error(t, err)
	var retry *UpstreamFailoverError
	require.ErrorAs(t, err, &retry)
	require.False(t, retry.ShouldRetryNextAccount(), "a late credential error must not replay an accepted generation on another account")
	require.False(t, retry.RetryableOnSameAccount)
	require.Equal(t, []string{"/prefix/token", "/prefix/vendor/chat"}, paths)
	require.NotContains(t, recorder.Body.String(), "upstream-fixture-key")
}

type providerCollectWaitReader struct {
	io.Reader
	once    sync.Once
	waiting chan struct{}
}

func (r *providerCollectWaitReader) Read(body []byte) (int, error) {
	r.once.Do(func() { close(r.waiting) })
	return r.Reader.Read(body)
}

func TestNewAPIProviderCollectDoesNotLeaseWorkersWhileReading(t *testing.T) {
	s, account, _ := providerFixture(t, providerCollectSource(false))
	exchange, err := s.buildProviderRequest(context.Background(), account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text","stream":false}`))
	require.NoError(t, err)
	var cancels []context.CancelFunc
	var outcomes []chan error
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	for range runner.MaxProcesses + 1 {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		reader, writer := io.Pipe()
		t.Cleanup(func() { writer.Close() })
		waiting := make(chan struct{})
		body := &newAPIProviderReadCloser{Reader: io.MultiReader(strings.NewReader("{\"text\":\"partial\"}\n"), &providerCollectWaitReader{Reader: reader, waiting: waiting}), Closer: reader}
		response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/x-ndjson"}}, Body: body}
		result := make(chan error, 1)
		outcomes = append(outcomes, result)
		pinned := *exchange
		go func() { result <- s.convertProviderResponse(ctx, &pinned, response, false) }()
		select {
		case <-waiting:
		case <-time.After(3 * time.Second):
			t.Fatal("an idle upstream retained a JS worker")
		}
	}
	for _, cancel := range cancels {
		cancel()
	}
	for _, result := range outcomes {
		select {
		case err := <-result:
			require.Error(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("cancelled collector did not close its HTTP body")
		}
	}
}
