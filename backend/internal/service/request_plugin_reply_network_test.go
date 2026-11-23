//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
)

const replyNetworkSource = requestNetworkMeta + `
export function ingress(){return {action:"continue"}}
export function reply(ctx,res){
 const checked=utils.fetch({url:ctx.config.url,method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({model:ctx.model,stage:ctx.stage,outputs:res.outputs})});
 if(checked.status!==200 || checked.body.allowed!==true)return {action:"stop",code:"blocked",message:"PRIVATE_REVIEW"};
 return {action:"continue"};
}`

func TestProviderReplyNetworkSeparateGrantPinnedAndNoCredentialLeak(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, stream := range []bool{false, true} {
			for _, granted := range []bool{false, true} {
				t.Run(path+"/stream="+strconv.FormatBool(stream)+"/grant="+strconv.FormatBool(granted), func(t *testing.T) {
					s, account, upstream, plan := providerPolicyFixture(t, providerConverterSource(), replyNetworkSource, []string{"ingress", "reply"}, path)
					plan.items[0].NetworkEnabled = true // Existing ingress access cannot authorize reply.
					plan.items[0].ReplyNetworkEnabled = granted
					repo := s.repo.(*requestPluginFixtureRepo)
					repo.items[0].ReplyNetworkEnabled = !granted
					repo.items[0].ConfigEncrypted = `ENC:{"url":"https://changed.example.com"}`
					h := &requestNetworkHTTP{inner: s.gateway.httpUpstream}
					s.gateway.httpUpstream = h
					upstream.response = `{"reply":"reviewed output","input":12,"output":5}`
					if stream {
						upstream.contentType = "text/event-stream"
						upstream.response = "data: {\"seq\":0,\"text\":\"reviewed output\"}\n\ndata: {\"seq\":1,\"finished\":true,\"input\":12,\"output\":5}\n\n"
					}
					body := []byte(`{"model":"public-text","messages":[],"input":"private client input","stream":` + strconv.FormatBool(stream) + `}`)
					c, recorder := providerContext(path, string(body))
					_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, path, "")
					if granted {
						require.NoError(t, err)
						require.Positive(t, h.calls.Load())
						require.True(t, h.closed.Load())
						require.Contains(t, recorder.Body.String(), "reviewed output")
						require.Contains(t, h.body, `"stage":"reply"`)
						for _, secret := range []string{"upstream-fixture-key", "downstream-secret", "private client input", "changed.example.com"} {
							require.NotContains(t, h.body, secret)
						}
					} else {
						require.Error(t, err)
						require.Zero(t, h.calls.Load())
						require.NotContains(t, recorder.Body.String(), "reviewed output")
					}
					require.Len(t, upstream.requests, 1, "review failures cannot retry or switch the generation account")
					audit, err := json.Marshal(repo.runs)
					require.NoError(t, err)
					require.NotContains(t, string(audit), "reviewed output")
				})
			}
		}
	}
}

func TestProviderReplyNetworkStopCannotBeMadeOptional(t *testing.T) {
	source := strings.Replace(replyNetworkSource, "apiVersion:1,", "apiVersion:1,optional:true,", 1)
	s, account, upstream, plan := providerPolicyFixture(t, providerConverterSource(), source, []string{"reply"}, newAPIProviderChat)
	plan.items[0].ReplyNetworkEnabled = true
	s.gateway.httpUpstream = &requestNetworkHTTP{inner: s.gateway.httpUpstream, response: `{"allowed":false}`}
	upstream.response = `{"reply":"blocked output","input":12,"output":5}`
	body := []byte(`{"model":"public-text","messages":[]}`)
	c, recorder := providerContext(newAPIProviderChat, string(body))
	result, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, newAPIProviderChat, "")
	var failure *RequestPluginFailure
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.Rejected)
	require.NotNil(t, result)
	require.Equal(t, 12, result.Usage.InputTokens, "already measured usage keeps the original billing path")
	require.NotContains(t, recorder.Body.String(), "blocked output")
	require.NotContains(t, recorder.Body.String(), "PRIVATE_REVIEW")
	require.Len(t, upstream.requests, 1)
}

func TestProviderReplyNetworkBoundsOfflineAndCancellation(t *testing.T) {
	for _, boundary := range []string{"capacity", "oversize", "calls", "cancel", "offline"} {
		t.Run(boundary, func(t *testing.T) {
			source := replyNetworkSource
			if boundary == "calls" {
				source = requestNetworkMeta + `export function reply(ctx){for(let i=0;i<5;i++)utils.fetch({url:ctx.config.url});return {action:"continue"}}`
			}
			s, _, _, plan := providerPolicyFixture(t, providerFixtureSource, source, []string{"reply"}, newAPIProviderChat)
			plan.items[0].ReplyNetworkEnabled = true
			h := &requestNetworkHTTP{}
			s.gateway.httpUpstream = h
			if boundary == "oversize" {
				h.response = strings.Repeat("x", jsplugin.MaxFetchBodyBytes+1)
			}
			budget := int64(requestPluginFetchMemory)
			if boundary == "capacity" {
				budget--
			}
			ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), pkghttputil.NewBodyMemoryBudget(budget))
			defer release()
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			if boundary == "offline" {
				ctx = context.WithValue(ctx, requestPluginOfflineKey{}, true)
			}
			instance := s.replyInstances(plan, plan.items, 17, "vendor-text", func() int { return 0 })[0]
			call := func() error {
				_, err := instance.Call(ctx, false, []jsplugin.ReplyOutput{{Text: "private output", Final: true}})
				return err
			}
			if boundary == "cancel" {
				h.block, h.started = true, make(chan struct{}, 1)
				finished := make(chan error, 1)
				go func() { finished <- call() }()
				select {
				case <-h.started:
				case <-time.After(2 * time.Second):
					t.Fatal("reply review did not start")
				}
				cancel()
				select {
				case err := <-finished:
					require.Error(t, err)
				case <-time.After(2 * time.Second):
					t.Fatal("reply cancellation retained the hook")
				}
			} else {
				require.Error(t, call())
			}
			switch boundary {
			case "capacity", "offline":
				require.Zero(t, h.calls.Load())
			case "calls":
				require.EqualValues(t, 4, h.calls.Load())
			case "oversize":
				require.EqualValues(t, 1, h.calls.Load())
				require.True(t, h.closed.Load())
			}
			require.NoError(t, pkghttputil.ReserveRequestBodyMemory(context.WithoutCancel(ctx), budget), "all temporary fetch memory is released")
		})
	}
}

func TestProviderReplyNetworkSaveAndDryRunStayIndependent(t *testing.T) {
	s, account, _, _ := providerPolicyFixture(t, providerFixtureSource, replyNetworkSource, []string{"reply"}, newAPIProviderChat)
	input := RequestPluginInstance{Name: "Reply review", PluginVersionID: 2, Scope: "account", AccountIDs: []int64{account.ID}, Formats: []string{"openai"}, Stages: []string{"reply"}, Config: json.RawMessage(`{}`), ReplyNetworkEnabled: true}
	saved, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.NoError(t, err)
	require.True(t, saved.ReplyNetworkEnabled)
	require.False(t, saved.NetworkEnabled)
	input.Stages = []string{"ingress"}
	_, err = s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.ErrorContains(t, err, "reply")
	h := &requestNetworkHTTP{}
	s.gateway.httpUpstream = h
	out, err := s.DryRunRequestPluginStage(context.Background(), 2, json.RawMessage(`{}`), json.RawMessage(`{"choices":[{"message":{"content":"private output"}}]}`), RequestPluginCall{Model: "public-text", Format: "openai"}, "reply")
	require.NoError(t, err)
	require.NotEmpty(t, out.ErrorClass)
	require.Zero(t, h.calls.Load(), "dry runs never inherit saved reply permissions")
}
