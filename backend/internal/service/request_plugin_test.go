//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

type requestPluginFixtureRepo struct {
	NewAPIPluginRepository
	RequestPluginRepository
	items    []RequestPluginInstance
	versions map[int64]*NewAPIPluginVersion
	runs     []RequestPluginRun
	listErr  error
	listWait <-chan struct{}
	started  chan struct{}
	reads    atomic.Int64
	runMu    sync.Mutex
	runErr   error
}

func (r *requestPluginFixtureRepo) Get(_ context.Context, id int64) (*NewAPIPluginVersion, error) {
	v := r.versions[id]
	if v == nil {
		return nil, ErrRequestPluginMissing
	}
	return v, nil
}
func (r *requestPluginFixtureRepo) ListRequestPluginInstances(ctx context.Context, execution bool) ([]RequestPluginInstance, error) {
	r.reads.Add(1)
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
	}
	if r.listWait != nil {
		select {
		case <-r.listWait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if r.listErr != nil {
		return nil, r.listErr
	}
	items := slices.Clone(r.items)
	if !execution {
		for i := range items {
			items[i].ConfigEncrypted = ""
			items[i].Version = nil
		}
	}
	return items, nil
}
func (r *requestPluginFixtureRepo) GetRequestPluginInstance(_ context.Context, id int64) (*RequestPluginInstance, error) {
	for _, item := range r.items {
		if item.ID == id {
			return &item, nil
		}
	}
	return nil, ErrRequestPluginMissing
}
func (r *requestPluginFixtureRepo) SaveRequestPluginInstance(_ context.Context, item *RequestPluginInstance, _ int64) (*RequestPluginInstance, error) {
	copy := *item
	copy.Version = r.versions[item.PluginVersionID]
	copy.Revision++
	if item.ID == 0 {
		copy.ID = int64(len(r.items) + 1)
		r.items = append(r.items, copy)
	} else {
		for i := range r.items {
			if r.items[i].ID == item.ID {
				r.items[i] = copy
			}
		}
	}
	copy.ConfigEncrypted = ""
	return &copy, nil
}
func (r *requestPluginFixtureRepo) RecordRequestPluginRun(_ context.Context, item RequestPluginRun) error {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	if r.runErr != nil {
		return r.runErr
	}
	r.runs = append(r.runs, item)
	return nil
}

func requestPluginFixture(t *testing.T, source string) (*NewAPIPluginService, *requestPluginFixtureRepo) {
	t.Helper()
	s, _, _ := generationPluginFixture(t, "sora")
	p, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	v := &NewAPIPluginVersion{ID: 1, Key: p.Meta.Key, Version: p.Meta.Version, Source: source, Meta: p.Meta, Capabilities: runner.Describe(p), Enabled: false}
	r := &requestPluginFixtureRepo{versions: map[int64]*NewAPIPluginVersion{1: v}}
	s.repo = r
	s.encryptor = pluginTokenEncryptor{}
	return s, r
}
func requestFixtureItem(id int64, scope string, config string, v *NewAPIPluginVersion) RequestPluginInstance {
	return RequestPluginInstance{ID: id, Name: "fixture", PluginVersionID: v.ID, Scope: scope, GroupIDs: []int64{7}, Stages: []string{"ingress"}, Enabled: true, Revision: 1, ConfigEncrypted: "ENC:" + config, Version: v}
}

const requestAppendSource = `export const meta={apiVersion:1,author:{name:"Fixture"},key:"scope-fixture",name:"Fixture",version:"1.0.0",configSchema:{type:"object",properties:{suffix:{type:"string",title:"Suffix",description:"Append text",default:"x"}}}};
export function ingress(ctx,req){return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/prompt",value:req.body.prompt+ctx.config.suffix}]}]};}`

func TestRequestPluginOrderScopePinnedSnapshotAndSingleIngress(t *testing.T) {
	s, r := requestPluginFixture(t, requestAppendSource)
	v := r.versions[1]
	r.items = []RequestPluginInstance{requestFixtureItem(1, "group", `{"suffix":"-group"}`, v), requestFixtureItem(2, "global", `{"suffix":"-global"}`, v), requestFixtureItem(3, "group", `{"suffix":"-other"}`, v)}
	r.items[2].GroupIDs = []int64{99}
	call := RequestPluginCall{GroupID: 7, Model: "video", Format: "task"}
	plan, err := s.NewRequestPluginPlan(context.Background(), call)
	require.NoError(t, err)
	require.NotNil(t, plan)
	r.items[0].ConfigEncrypted = `ENC:{"suffix":"-changed"}`
	body := []byte(`{"model":"video","prompt":"hello","stream":false}`)
	out, err := plan.Ingress(context.Background(), body)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"video","prompt":"hello-global-group","stream":false}`, string(out.Body))
	require.Equal(t, int64(2), out.Records[0].InstanceID)
	require.Equal(t, int64(1), out.Records[1].InstanceID)
	require.False(t, v.Enabled, "request instances do not require the Task/Provider switch")
	_, err = plan.Ingress(context.Background(), out.Body)
	require.ErrorContains(t, err, "already executed")
	// Match every decoder candidate: a duplicate field cannot bypass a
	// model-scoped policy by placing an unrelated first model before it.
	r.items[0].Models = []string{"restricted"}
	r.items = r.items[:1]
	s.requestPlugins.expires = s.requestPlugins.expires.Add(-requestPluginCacheTTL)
	call.Model = "first"
	call.ModelCandidates = []string{"first", "restricted"}
	plan, err = s.NewRequestPluginPlan(context.Background(), call)
	require.NoError(t, err)
	require.NotNil(t, plan)
	call.ModelCandidates = []string{"unmatched"}
	plan, err = s.NewRequestPluginPlan(context.Background(), call)
	require.NoError(t, err)
	require.Nil(t, plan)
}

func TestRequestPluginPatchGuardsAndOptionalFailure(t *testing.T) {
	for _, test := range []struct{ name, effect string }{
		{"model", `[{type:"json.patch",ops:[{op:"replace",path:"/model",value:"expensive"}]}]`},
		{"stream", `[{type:"json.patch",ops:[{op:"add",path:"/stream",value:true}]}]`},
		{"case variant", `[{type:"json.patch",ops:[{op:"add",path:"/MODEL",value:"expensive"}]}]`},
		{"headers", `[{type:"header.patch",set:{Authorization:"stolen"}}]`},
		{"atomic", `[{type:"json.patch",ops:[{op:"replace",path:"/prompt",value:"changed"},{op:"remove",path:"/missing"}]}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `export const meta={apiVersion:1,author:{name:"Fixture"},key:"guard-fixture",name:"Guard",version:"1.0.0"};export function ingress(){return {action:"continue",effects:` + test.effect + `};}`
			s, r := requestPluginFixture(t, source)
			v := r.versions[1]
			r.items = []RequestPluginInstance{requestFixtureItem(1, "global", `{}`, v)}
			body := []byte(`{"model":"safe","prompt":"original"}`)
			plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{Model: "safe"})
			require.NoError(t, err)
			out, err := plan.Ingress(context.Background(), body)
			require.Error(t, err)
			require.Equal(t, string(body), string(out.Body))
			require.Equal(t, "error", out.Records[0].Action)
			// Optional failures skip this instance atomically, including guard errors.
			v.Meta.Optional = true
			plan, err = s.NewRequestPluginPlan(context.Background(), RequestPluginCall{Model: "safe"})
			require.NoError(t, err)
			out, err = plan.Ingress(context.Background(), body)
			require.NoError(t, err)
			require.Equal(t, string(body), string(out.Body))
			require.Equal(t, "skipped", out.Records[0].Action)
		})
	}
}

func TestRequestPluginBuiltinsFilterRedactAndAuditPrivacy(t *testing.T) {
	for _, key := range []string{"request-filter", "request-redact"} {
		t.Run(key, func(t *testing.T) {
			source, err := builtin.Source(key)
			require.NoError(t, err)
			s, r := requestPluginFixture(t, source)
			config := json.RawMessage(`{"rules":[{"pattern":"secret-needle"}]}`)
			if key == "request-redact" {
				config = json.RawMessage(`{"finalCheck":"off"}`)
			}
			validated, err := s.validateRequestPluginConfig(context.Background(), r.versions[1], config)
			require.NoError(t, err)
			r.items = []RequestPluginInstance{requestFixtureItem(1, "global", string(validated), r.versions[1])}
			call := RequestPluginCall{RequestID: "privacy-test", Model: "gpt-fixture", Format: "openai"}
			body := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"secret-needle: person@example.com"}]}`)
			plan, err := s.NewRequestPluginPlan(context.Background(), call)
			require.NoError(t, err)
			out, err := plan.Ingress(context.Background(), body)
			if key == "request-filter" {
				var failure *RequestPluginFailure
				require.ErrorAs(t, err, &failure)
				require.True(t, failure.Rejected)
			} else {
				require.NoError(t, err)
				require.NotContains(t, string(out.Body), "person@example.com")
				require.Contains(t, string(out.Body), "[REDACTED]")
			}
			require.NoError(t, s.RecordRequestPluginRun(context.Background(), call, out, false))
			raw, err := json.Marshal(r.runs)
			require.NoError(t, err)
			for _, secret := range []string{"person@example.com", "secret-needle", "messages", "content", "pattern", "ConfigEncrypted"} {
				require.NotContains(t, string(raw), secret)
			}
		})
	}
}

func TestRequestPluginSaveEncryptsConfigAndRequiresExplicitStage(t *testing.T) {
	s, r := requestPluginFixture(t, requestAppendSource)
	input := RequestPluginInstance{Name: "test", PluginVersionID: 1, Scope: "group", GroupIDs: []int64{7}, Stages: []string{"ingress"}, Config: json.RawMessage(`{"suffix":"secret-config"}`)}
	saved, err := s.SaveRequestPluginInstance(context.Background(), input, 9)
	require.NoError(t, err)
	require.Empty(t, saved.Config)
	require.Empty(t, saved.ConfigEncrypted)
	require.Contains(t, r.items[0].ConfigEncrypted, "ENC:")
	input.ID = saved.ID
	input.Revision = saved.Revision
	input.PluginVersionID = 999
	_, err = s.SaveRequestPluginInstance(context.Background(), input, 9)
	require.ErrorContains(t, err, "实例固定插件版本")
	input.PluginVersionID = 1
	input.Config = nil
	input.Enabled = true
	_, err = s.SaveRequestPluginInstance(context.Background(), input, 9)
	require.NoError(t, err)
	require.Contains(t, r.items[0].ConfigEncrypted, "secret-config")
	_, err = s.SaveRequestPluginInstance(context.Background(), input, 9)
	require.ErrorIs(t, err, ErrRequestPluginConflict)
	input.ID = 0
	input.Revision = 0
	input.Stages = []string{"outbound"}
	_, err = s.SaveRequestPluginInstance(context.Background(), input, 9)
	require.Error(t, err)
	input.Stages = []string{"ingress"}
	input.Models = []string{"*"}
	_, err = s.SaveRequestPluginInstance(context.Background(), input, 9)
	require.Error(t, err)
}

func TestRequestPluginFailureNeverLeaksExceptionAndCancellation(t *testing.T) {
	s, r := requestPluginFixture(t, `export const meta={apiVersion:1,author:{name:"Fixture"},key:"throw-fixture",name:"Throw",version:"1.0.0"};export function ingress(){throw new Error("secret-exception");}`)
	r.items = []RequestPluginInstance{requestFixtureItem(1, "global", `{}`, r.versions[1])}
	plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{Model: "m"})
	require.NoError(t, err)
	out, err := plan.Ingress(context.Background(), []byte(`{"model":"m"}`))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-exception")
	raw, _ := json.Marshal(out.Records)
	require.NotContains(t, string(raw), "secret-exception")
	// Canceled requests never run, even if an optional plugin could be skipped.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.NewRequestPluginPlan(canceled, RequestPluginCall{Model: "m"})
	if err == nil {
		plan, _ = s.NewRequestPluginPlan(context.Background(), RequestPluginCall{Model: "m"})
		_, err = plan.Ingress(canceled, []byte(`{"model":"m"}`))
	}
	require.Error(t, err)
	_, err = s.DryRunRequestPlugin(context.Background(), 1, json.RawMessage(`{}`), json.RawMessage(`{"model":"m"}`), RequestPluginCall{Model: "m"})
	// A validated dry run returns a safe error class in its outcome.
	require.NoError(t, err)
}

func TestRequestPluginIngressAndDryRunCannotFetch(t *testing.T) {
	s, r := requestPluginFixture(t, `export const meta={apiVersion:1,author:{name:"Fixture"},key:"fetch-fixture",name:"Fetch",version:"1.0.0"};export function ingress(){utils.fetch({url:"https://api.example.com/forbidden"});return {action:"continue"};}`)
	r.items = []RequestPluginInstance{requestFixtureItem(1, "global", `{}`, r.versions[1])}
	httpClient := s.gateway.httpUpstream.(*generationFixtureHTTP)
	plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{Model: "m"})
	require.NoError(t, err)
	out, err := plan.Ingress(context.Background(), []byte(`{"model":"m"}`))
	require.Error(t, err)
	require.Equal(t, "error", out.Records[0].Action)
	require.Empty(t, httpClient.requests)
	out, err = s.DryRunRequestPlugin(context.Background(), 1, json.RawMessage(`{}`), json.RawMessage(`{"model":"m"}`), RequestPluginCall{Model: "m"})
	require.NoError(t, err)
	require.Equal(t, "error", out.Records[0].Action)
	require.Empty(t, httpClient.requests, "neither ingress nor dry-run may reuse Task fetch credentials")
}

func TestRequestPluginExpiredPolicyIsNotReusedAfterRefreshFailure(t *testing.T) {
	s, r := requestPluginFixture(t, requestAppendSource)
	r.items = []RequestPluginInstance{requestFixtureItem(1, "global", `{}`, r.versions[1])}
	plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{Model: "m"})
	require.NoError(t, err)
	require.NotNil(t, plan)
	r.listErr = errors.New("fixture policy database unavailable")
	s.requestPlugins.expires = time.Time{}
	plan, err = s.NewRequestPluginPlan(context.Background(), RequestPluginCall{Model: "m"})
	require.ErrorIs(t, err, r.listErr)
	require.Nil(t, plan, "expired security rules must not silently keep authorizing new requests")
}

func TestRequestPluginRejectsUnavailableReplyAndEgressConfig(t *testing.T) {
	for _, tc := range []struct{ key, config string }{
		{"request-filter", `{"replies":true}`},
		{"request-redact", `{"replies":true,"finalCheck":"off"}`},
		{"request-redact", `{}`},
	} {
		t.Run(tc.key+tc.config, func(t *testing.T) {
			source, err := builtin.Source(tc.key)
			require.NoError(t, err)
			s, r := requestPluginFixture(t, source)
			_, err = s.SaveRequestPluginInstance(context.Background(), RequestPluginInstance{Name: "Unsupported control", PluginVersionID: 1, Scope: "global", Stages: []string{"ingress"}, Config: json.RawMessage(tc.config)}, 9)
			require.Error(t, err)
			require.Empty(t, r.items)
		})
	}
}

func TestRequestPluginRefreshIsSharedAndCanceledCallerStopsWaiting(t *testing.T) {
	s, r := requestPluginFixture(t, requestAppendSource)
	r.items = []RequestPluginInstance{requestFixtureItem(1, "global", `{}`, r.versions[1])}
	release := make(chan struct{})
	r.listWait = release
	r.started = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := s.NewRequestPluginPlan(ctx, RequestPluginCall{Model: "m"}); first <- err }()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("policy refresh did not start")
	}
	cancel()
	select {
	case err := <-first:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled request kept waiting behind the policy database")
	}
	second := make(chan error, 1)
	go func() {
		plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{Model: "m"})
		if err == nil && plan == nil {
			err = errors.New("policy snapshot was lost")
		}
		second <- err
	}()
	close(release)
	select {
	case err := <-second:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("second request did not receive the shared policy snapshot")
	}
	require.EqualValues(t, 1, r.reads.Load(), "one slow refresh must not become a queue of serial database reads")
}
