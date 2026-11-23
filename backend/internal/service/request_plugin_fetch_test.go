//go:build unit

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/mediahost"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const requestNetworkURL = "https://policy.example.com/check"
const requestNetworkMeta = `export const meta={apiVersion:1,key:"network-policy",name:"Policy service",version:"1.0.0",author:{name:"Fixture"},configSchema:{type:"object",properties:{url:{type:"string",title:"URL",description:"Moderation service",format:"url",default:"https://policy.example.com/check"}}}};`
const requestNetworkCall = `const res=utils.fetch({url:ctx.config.url,method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({model:ctx.model,prompt:req.body.prompt})});if(res.status!==200 || res.body.allowed!==true)return {action:"reject",code:"denied",message:"PRIVATE"};`

func requestNetworkSource(stage string) string {
	result := `return {action:"continue"};`
	if stage == "ingress" {
		result = `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/checked",value:true}]}]};`
	}
	return requestNetworkMeta + `export function ` + stage + `(ctx,req){` + requestNetworkCall + result + `}`
}

type requestNetworkBody struct {
	io.Reader
	closed *atomic.Bool
}

func (b requestNetworkBody) Close() error { b.closed.Store(true); return nil }

type requestNetworkHTTP struct {
	HTTPUpstream
	inner    HTTPUpstream
	allCalls atomic.Int32
	calls    atomic.Int32
	closed   atomic.Bool
	started  chan struct{}
	block    bool
	response string
	body     string
}

func (h *requestNetworkHTTP) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	h.allCalls.Add(1)
	if req.URL.Host != "policy.example.com" {
		if h.inner != nil {
			return h.inner.Do(req, proxy, id, concurrency)
		}
		return nil, errors.New("unapproved service request")
	}
	h.calls.Add(1)
	if proxy != "" || id != 0 || concurrency != 2 || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" || !HTTPUpstreamPublicHostsOnly(req.Context()) || !HTTPUpstreamRedirectsDisabled(req.Context()) {
		return nil, errors.New("host context or credentials leaked")
	}
	deadline, ok := req.Context().Deadline()
	if !ok || time.Until(deadline) > 5*time.Second {
		return nil, errors.New("stage deadline missing")
	}
	if h.started != nil {
		h.started <- struct{}{}
	}
	if h.block {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		h.body = string(raw)
	}
	body := h.response
	if body == "" {
		body = `{"allowed":true}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: requestNetworkBody{Reader: strings.NewReader(body), closed: &h.closed}}, nil
}

func requestNetworkFixture(t *testing.T) (*NewAPIPluginService, *requestPluginFixtureRepo, *requestNetworkHTTP) {
	t.Helper()
	s, repo := requestPluginFixture(t, requestNetworkSource("ingress"))
	item := requestFixtureItem(1, "global", `{"url":"`+requestNetworkURL+`"}`, repo.versions[1])
	item.NetworkEnabled = true
	repo.items = []RequestPluginInstance{item}
	h := &requestNetworkHTTP{}
	s.gateway.httpUpstream = h
	return s, repo, h
}

func TestRequestPluginStageNetworkExplicitOptInAndSafeProjection(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default offline", true: "explicit opt in"}[enabled], func(t *testing.T) {
			s, repo, h := requestNetworkFixture(t)
			repo.items[0].NetworkEnabled = enabled
			plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{GroupID: 7, Model: "public-model", Format: "openai"})
			require.NoError(t, err)
			// Later administrative edits cannot change the request-pinned grant.
			repo.items[0].NetworkEnabled = !enabled
			repo.items[0].ConfigEncrypted = `ENC:{"url":"https://other.example.com"}`
			out, err := plan.Ingress(context.Background(), []byte(`{"model":"public-model","prompt":"hello"}`))
			if !enabled {
				require.Error(t, err)
				require.Zero(t, h.calls.Load())
				return
			}
			require.NoError(t, err)
			require.True(t, gjson.GetBytes(out.Body, "checked").Bool())
			require.EqualValues(t, 1, h.calls.Load())
			require.True(t, h.closed.Load())
			require.JSONEq(t, `{"model":"public-model","prompt":"hello"}`, h.body)
			audit, err := json.Marshal(out.Records)
			require.NoError(t, err)
			for _, value := range []string{"policy.example.com", "hello", "PRIVATE"} {
				require.NotContains(t, string(audit), value)
			}
		})
	}
}

func TestRequestPluginInlineMediaToolsAndAuthorizedStreaming(t *testing.T) {
	for _, stage := range []string{"ingress", "egress"} {
		t.Run(stage, func(t *testing.T) {
			s, repo, h := requestNetworkFixture(t)
			source := requestNetworkMeta + `export function ` + stage + `(ctx,req){
 const a=utils.attachments(); if(a.length!==1 || a[0].ref!=="[media:0]" || req.body.file!==a[0].ref)throw Error("projection");
 const m=utils.media(a[0].ref),text=utils.mediaText(a[0].ref);if(m.mime!=="text/plain" || text.text!=="Hello")throw Error("metadata");
 const r=utils.fetch({url:ctx.config.url,method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({file:a[0].ref})});
 if(r.status!==200)throw Error("fetch");return {action:"continue"};}`
			loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
			require.NoError(t, err)
			repo.versions[1].Source, repo.versions[1].Meta = source, loaded.Meta
			body := []byte(`{"model":"public-model","file":"data:text/plain;base64,SGVsbG8="}`)
			media, err := mediahost.New(body)
			require.NoError(t, err)
			err = s.pool.With(context.Background(), source, func(client *runner.Client) error {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, err := s.callRequestPluginTransform(ctx, client, true, jsplugin.TransformContext{Stage: stage, Format: "openai", Config: json.RawMessage(`{"url":"` + requestNetworkURL + `"}`), Media: media}, jsplugin.TransformRequest{Body: media.View(), Media: media.Projection()})
				return err
			})
			require.NoError(t, err)
			require.EqualValues(t, 1, h.calls.Load())
			require.JSONEq(t, `{"file":"data:text/plain;base64,SGVsbG8="}`, h.body)
			require.True(t, h.closed.Load())
		})
	}
}

func TestRequestPluginEarlyIngressSeesMediaProjectionAndRetainsOriginalBody(t *testing.T) {
	source := requestNetworkMeta + `export function ingress(ctx,req){if(req.body.file!=="[media:0]" || utils.attachments()[0].ref!==req.body.file || utils.mediaText(req.body.file).text!=="Hello")throw Error("projection");return {action:"continue"}}`
	s, repo := requestPluginFixture(t, source)
	repo.items = []RequestPluginInstance{requestFixtureItem(1, "global", `{}`, repo.versions[1])}
	plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{Model: "public-model", Format: "openai"})
	require.NoError(t, err)
	body := []byte(`{"model":"public-model","file":"data:text/plain;base64,SGVsbG8="}`)
	out, err := plan.Ingress(context.Background(), body)
	require.NoError(t, err)
	require.Equal(t, body, []byte(out.Body))
	audit, err := json.Marshal(out.Records)
	require.NoError(t, err)
	require.NotContains(t, string(audit), "SGVsbG8=")
}

func TestProviderMediaTextEffectsChangeActualWireAndAuditPartialReads(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "full text", true: "partial retains original"}[partial], func(t *testing.T) {
			read := `utils.mediaText("[media:0]")`
			if partial {
				read = `utils.mediaText("[media:0]",{maxChars:2})`
			}
			policy := providerPolicyMeta + `export function outbound(ctx,req){` + read + `;return {action:"continue",effects:[{type:"media.text",media:0,text:"redacted"}]}}`
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, policy, []string{"outbound"}, newAPIProviderChat)
			body := `{"model":"public-text","messages":[],"file":{"filename":"private-file.txt","file_data":"data:text/plain;base64,SGVsbG8="}}`
			c, recorder := providerContext(newAPIProviderChat, body)
			_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
			require.NoError(t, err, recorder.Body.String())
			require.Len(t, upstream.requests, 1)
			wire, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			want := "data:text/plain;base64,cmVkYWN0ZWQ="
			if partial {
				want = "data:text/plain;base64,SGVsbG8="
			}
			require.Equal(t, want, gjson.GetBytes(wire, "file.file_data").String())
			runs := s.repo.(*requestPluginFixtureRepo).runs
			require.Len(t, runs, 1)
			require.Len(t, runs[0].Records[0].Attachments, 1)
			a := runs[0].Records[0].Attachments[0]
			require.Equal(t, !partial, a.Rewritten)
			if partial {
				require.Equal(t, "not_rewritable", a.Reason)
			}
			audit, err := json.Marshal(runs)
			require.NoError(t, err)
			require.NotContains(t, string(audit), "private-file")
			require.NotContains(t, string(audit), "SGVsbG8")
			require.NotContains(t, string(audit), "redacted")
		})
	}
}

func TestProviderMediaImageEffectsReachSignedUpstream(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 256, 128))
	for y := range 128 {
		for x := range 256 {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y * 2), B: 50, A: 255})
		}
	}
	var original bytes.Buffer
	require.NoError(t, (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&original, img))
	encoded := base64.StdEncoding.EncodeToString(original.Bytes())
	for _, format := range []string{"jpeg", "png", "webp"} {
		t.Run(format, func(t *testing.T) {
			policy := providerPolicyMeta + `export function outbound(ctx,req){
 if(req.media.length!==1 || utils.media("[media:0]").width!==256)throw Error("image projection");
 return {action:"continue",effects:[{type:"media.reencode",media:0,format:"` + format + `",maxSide:64}]};}`
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, policy, []string{"outbound"}, newAPIProviderChat)
			body := `{"model":"public-text","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + encoded + `"}}]}]}`
			c, recorder := providerContext(newAPIProviderChat, body)
			_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
			require.NoError(t, err, recorder.Body.String())
			require.Len(t, upstream.requests, 1)
			wire, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			mediaType, payload, found := strings.Cut(gjson.GetBytes(wire, "messages.0.content.0.image_url.url").String(), ",")
			require.True(t, found)
			require.Equal(t, "data:image/"+format+";base64", mediaType)
			raw, err := base64.StdEncoding.DecodeString(payload)
			require.NoError(t, err)
			config, actualFormat, err := image.DecodeConfig(bytes.NewReader(raw))
			require.NoError(t, err)
			require.Equal(t, format, actualFormat)
			require.Equal(t, 64, config.Width)
			require.Equal(t, 32, config.Height)
			require.Less(t, len(raw), original.Len())
			digest := sha256.Sum256(wire)
			require.Equal(t, hex.EncodeToString(digest[:]), upstream.requests[0].Header.Get("X-Body-Digest"))
			runs := s.repo.(*requestPluginFixtureRepo).runs
			require.Len(t, runs, 1)
			require.Len(t, runs[0].Records[0].Media, 1)
			require.Equal(t, len(raw), runs[0].Records[0].Media[0].To.Bytes)
			audit, err := json.Marshal(runs)
			require.NoError(t, err)
			require.NotContains(t, string(audit), encoded)
			require.NotContains(t, string(audit), payload)
		})
	}
}

func TestRequestPluginIngressMediaRewriteAndRoutingGuardAreAtomic(t *testing.T) {
	for _, guard := range []bool{false, true} {
		source := providerPolicyMeta + `export function ingress(){return {action:"continue",effects:[{type:"media.text",media:0,text:"redacted"}`
		if guard {
			source += `,{type:"json.patch",ops:[{op:"replace",path:"/model",value:"private"}]}`
		}
		source += `]}}`
		s, repo := requestPluginFixture(t, source)
		repo.items = []RequestPluginInstance{requestFixtureItem(1, "global", `{}`, repo.versions[1])}
		plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{Model: "public-model", Format: "openai"})
		require.NoError(t, err)
		body := []byte(`{"model":"public-model","file":"data:text/plain;base64,SGVsbG8="}`)
		out, err := plan.Ingress(context.Background(), body)
		if guard {
			require.Error(t, err)
			require.JSONEq(t, string(body), string(out.Body))
			require.Empty(t, out.Records[0].Attachments)
		} else {
			require.NoError(t, err)
			require.Equal(t, "data:text/plain;base64,cmVkYWN0ZWQ=", gjson.GetBytes(out.Body, "file").String())
			require.True(t, out.Records[0].Attachments[0].Rewritten)
		}
	}
}

func TestRequestPluginTextBudgetIsSharedByAllIngressInstances(t *testing.T) {
	source := `export const meta={apiVersion:1,key:"media-budget",name:"Budget",version:"1.0.0",author:{name:"Fixture"},configSchema:{type:"object",properties:{index:{type:"integer",title:"Attachment",description:"Attachment index",default:0}}}};
export function ingress(ctx){
 const i=ctx.config.index,t=utils.mediaText("[media:"+i+"]");
 if(i<5 && (t.text.length!==100000 || t.truncated || t.unchecked))throw Error("full");
 if(i===5 && (t.text.length!==24288 || !t.truncated))throw Error("truncated");
 if(i===6 && t.unchecked!=="limit")throw Error("shared budget");
 return {action:"continue"};}`
	s, repo := requestPluginFixture(t, source)
	var files []string
	for i := range 7 {
		config, err := json.Marshal(map[string]int{"index": i})
		require.NoError(t, err)
		repo.items = append(repo.items, requestFixtureItem(int64(i+1), "global", string(config), repo.versions[1]))
		files = append(files, "data:text/plain;base64,"+base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(rune('a'+i)), 100000))))
	}
	body, err := json.Marshal(map[string]any{"model": "public-model", "files": files})
	require.NoError(t, err)
	plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{Model: "public-model", Format: "openai"})
	require.NoError(t, err)
	out, err := plan.Ingress(context.Background(), body)
	require.NoError(t, err)
	require.Len(t, out.Records, 7)
	require.Equal(t, "truncated", out.Records[5].Attachments[0].Status)
	require.Equal(t, "limit", out.Records[6].Attachments[0].Reason)
	require.JSONEq(t, string(body), string(out.Body))
}

func TestRequestPluginStageNetworkBoundsAndCancellation(t *testing.T) {
	for _, boundary := range []string{"capacity", "oversize", "cancel", "calls"} {
		t.Run(boundary, func(t *testing.T) {
			s, repo, h := requestNetworkFixture(t)
			if boundary == "oversize" {
				h.response = strings.Repeat("x", jsplugin.MaxFetchBodyBytes+1)
			}
			if boundary == "calls" {
				source := requestNetworkMeta + `export function ingress(ctx){for(let i=0;i<5;i++)utils.fetch({url:ctx.config.url});return {action:"continue"}}`
				loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
				require.NoError(t, err)
				repo.versions[1].Source, repo.versions[1].Meta = source, loaded.Meta
			}
			plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{Model: "m", Format: "openai"})
			require.NoError(t, err)
			ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), pkghttputil.NewBodyMemoryBudget(requestPluginFetchMemory-1))
			if boundary != "capacity" {
				release()
				ctx, release = pkghttputil.WithRequestBodyMemoryBudget(context.Background(), pkghttputil.NewBodyMemoryBudget(64<<20))
			}
			defer release()
			if boundary == "cancel" {
				h.block, h.started = true, make(chan struct{}, 1)
				canceled, cancel := context.WithCancel(ctx)
				defer cancel()
				finished := make(chan error, 1)
				go func() { _, err := plan.Ingress(canceled, []byte(`{"model":"m"}`)); finished <- err }()
				select {
				case <-h.started:
				case <-time.After(5 * time.Second):
					t.Fatal("service request did not start")
				}
				cancel()
				select {
				case err := <-finished:
					require.Error(t, err)
				case <-time.After(5 * time.Second):
					t.Fatal("canceled fetch retained the hook")
				}
				require.NoError(t, pkghttputil.ReserveRequestBodyMemory(ctx, 64<<20), "cancellation releases hook fetch admission")
				return
			}
			out, err := plan.Ingress(ctx, []byte(`{"model":"m"}`))
			require.Error(t, err)
			require.Equal(t, "error", out.Records[0].Action)
			switch boundary {
			case "capacity":
				require.Equal(t, "capacity", out.Records[0].ErrorClass)
				require.Zero(t, h.calls.Load())
			case "oversize":
				require.EqualValues(t, 1, h.calls.Load())
				require.True(t, h.closed.Load())
			case "calls":
				require.EqualValues(t, 4, h.calls.Load())
			}
		})
	}
}

func TestRequestPluginStageNetworkDryRunAndOtherStagesRemainOffline(t *testing.T) {
	s, _, h := requestNetworkFixture(t)
	for _, stage := range []string{"ingress", "request", "outbound", "egress"} {
		for _, offline := range []bool{false, true} {
			if !offline && (stage == "ingress" || stage == "egress") {
				continue
			}
			source := requestNetworkMeta + `export function ` + stage + `(ctx){utils.fetch({url:ctx.config.url});return {action:"continue"}}`
			ctx := context.WithValue(context.Background(), requestPluginOfflineKey{}, offline)
			err := s.pool.With(ctx, source, func(client *runner.Client) error {
				_, err := s.callRequestPluginTransform(ctx, client, true, jsplugin.TransformContext{Stage: stage, Format: "openai", Config: json.RawMessage(`{"url":"` + requestNetworkURL + `"}`)}, jsplugin.TransformRequest{Body: json.RawMessage(`{}`)})
				return err
			})
			require.Error(t, err)
			require.Zero(t, h.calls.Load())
		}
	}
	out, err := s.DryRunRequestPluginStage(context.Background(), 1, json.RawMessage(`{"url":"`+requestNetworkURL+`"}`), json.RawMessage(`{"model":"m"}`), RequestPluginCall{Model: "m", Format: "openai"}, "ingress")
	require.NoError(t, err)
	require.Equal(t, "execution", out.ErrorClass)
	require.Zero(t, h.calls.Load())
}

func TestRequestPluginStageNetworkRejectsUnapprovedTargets(t *testing.T) {
	s, _, h := requestNetworkFixture(t)
	for _, address := range []string{"https://other.example.com/check", "https://name:secret@policy.example.com/check", "https://policy.example.com/check#fragment", "file:///etc/passwd", "http://127.0.0.1/check"} {
		_, err := s.requestPluginFetcher([]string{"policy.example.com"})(context.Background(), jsplugin.FetchRequest{URL: address})
		require.Error(t, err)
	}
	require.Zero(t, h.allCalls.Load(), "invalid or undeclared targets never reach HTTP transport")
}

func TestRequestPluginStageNetworkSaveRequiresSupportedStage(t *testing.T) {
	s, _, _ := requestNetworkFixture(t)
	input := RequestPluginInstance{Name: "Moderation", PluginVersionID: 1, Scope: "global", Stages: []string{"ingress"}, Config: json.RawMessage(`{}`), NetworkEnabled: true}
	saved, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.NoError(t, err)
	require.True(t, saved.NetworkEnabled)
	input.Stages, input.Formats = []string{"report"}, []string{"task"}
	_, err = s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.ErrorContains(t, err, "ingress")
}

func TestProviderRequestPluginNetworkOnlyIngressAndEgressBeforeGeneration(t *testing.T) {
	for _, stage := range []string{"ingress", "egress"} {
		t.Run(stage, func(t *testing.T) {
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, requestNetworkSource(stage), []string{stage}, newAPIProviderChat)
			plan.items[0].NetworkEnabled = true
			plan.items[0].Config = json.RawMessage(`{"url":"` + requestNetworkURL + `"}`)
			h := &requestNetworkHTTP{inner: s.gateway.httpUpstream}
			s.gateway.httpUpstream = h
			body := `{"model":"public-text","messages":[],"prompt":"hello"}`
			c, _ := providerContext(newAPIProviderChat, body)
			result, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
			require.NoError(t, err)
			require.Equal(t, 5, result.Usage.OutputTokens)
			require.EqualValues(t, 1, h.calls.Load())
			require.Len(t, upstream.requests, 1)
			require.NotContains(t, h.body, "upstream-fixture-key")
			require.NotContains(t, h.body, "downstream-secret")
		})
	}
}

func TestTaskEgressNetworkGrantPinnedAndMultipartUnchecked(t *testing.T) {
	for _, granted := range []bool{false, true} {
		t.Run(map[bool]string{false: "old snapshot remains offline", true: "accepted snapshot keeps grant"}[granted], func(t *testing.T) {
			s, repo, account, task := taskEgressFixture(t, requestNetworkSource("egress"))
			repo.items[0].NetworkEnabled = granted
			repo.items[0].ConfigEncrypted = `ENC:{"url":"` + requestNetworkURL + `"}`
			s.requestPlugins.expires = time.Time{}
			call := RequestPluginCall{UserID: task.UserID, TokenID: task.APIKeyID, GroupID: task.GroupID, Model: task.Model, Format: "task"}
			plan, err := s.NewRequestPluginPlan(context.Background(), call)
			require.NoError(t, err)
			task.RequestPluginsEncrypted, err = s.snapshotTaskRequestPlugins(WithRequestPluginPlan(context.Background(), plan), &APIKey{ID: task.APIKeyID, UserID: task.UserID, Group: &Group{ID: task.GroupID}}, task.Model)
			require.NoError(t, err)
			repo.items[0].NetworkEnabled = !granted
			repo.items[0].ConfigEncrypted = `ENC:{"url":"https://edited.example.com"}`
			s.gateway.httpUpstream.(*generationFixtureHTTP).response = `{"id":"vendor-accepted"}`
			h := &requestNetworkHTTP{inner: s.gateway.httpUpstream}
			s.gateway.httpUpstream = h
			require.NoError(t, s.taskRequestPluginEgress(context.Background(), task, nil, true))
			require.Zero(t, h.calls.Load(), "multipart is never read or sent for moderation")
			err = s.SubmitTask(context.Background(), account, task)
			if granted {
				require.NoError(t, err)
				require.EqualValues(t, 1, h.calls.Load())
			} else {
				require.Error(t, err)
				require.Zero(t, h.calls.Load())
			}
		})
	}
}
