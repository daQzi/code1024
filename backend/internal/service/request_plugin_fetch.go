package service

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/newapi/mediahost"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

type requestPluginOfflineKey struct{}

// Fetch replies, their RPC/base64 encoding and temporary host copies are kept
// only while Typed is running. The existing stage reservation owns the final
// transform output after this scope closes; this is accounting, not an RSS cap.
const requestPluginFetchMemory = 16 << 20

// Transform ingress/egress and separately authorized reply checks may reach
// instance services. Hosts come from actual pinned source and validated URL
// fields; channel credentials and downstream headers/proxy are never attached.
func (s *NewAPIPluginService) callRequestPluginTransform(ctx context.Context, client *runner.Client, enabled bool, call jsplugin.TransformContext, request jsplugin.TransformRequest) ([]json.RawMessage, error) {
	if call.Media == nil && len(request.Body) != 0 && !request.Form {
		source, err := mediahost.New(request.Body)
		if err != nil {
			return nil, err
		}
		call.Media, request.Body, request.Media = source, source.View(), source.Projection()
	}
	return s.callRequestPluginStage(ctx, client, enabled, call, "CallTransform", request)
}

func (s *NewAPIPluginService) callRequestPluginStage(ctx context.Context, client *runner.Client, enabled bool, call jsplugin.TransformContext, method string, args ...any) ([]json.RawMessage, error) {
	args = append([]any{call}, args...)
	offline, _ := ctx.Value(requestPluginOfflineKey{}).(bool)
	allowed := method == "CallTransform" && (call.Stage == "ingress" || call.Stage == "egress") || method == "CallReply" && call.Stage == "reply"
	if !enabled || offline || !allowed {
		return client.Typed(ctx, nil, method, args...)
	}
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, requestPluginFetchMemory); err != nil {
		return nil, err
	}
	hosts := append(slices.Clone(client.Meta.AllowedHosts), client.Meta.ConfigSchema.URLHosts(call.Config)...)
	if len(hosts) == 0 {
		return client.Typed(ctx, nil, method, args...)
	}
	return client.Typed(ctx, s.requestPluginFetcher(hosts), method, args...)
}
