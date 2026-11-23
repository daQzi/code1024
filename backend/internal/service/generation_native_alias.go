package service

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/taskprotocol"
	"github.com/tidwall/gjson"
)

// These namespaces are owned by the host even for currently unregistered
// methods. /api/v3 (Doubao/Ark) and /api/common/v3 (Jimeng) are vendor Task APIs.
func NativeTaskAliasPathAllowed(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\%\x00") || strings.Contains(path, "//") || len(path) > 2048 {
		return false
	}
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	switch first {
	case "", ".well-known", "backend-api", "antigravity", "health", "readyz", "metrics",
		"setup", "admin", "auth", "oauth", "assets", "static", "debug", "swagger":
		return false
	case "api":
		return strings.HasPrefix(path, "/api/v3/") || strings.HasPrefix(path, "/api/common/v3/")
	}
	return path != "/v1/tasks" && !strings.HasPrefix(path, "/v1/tasks/")
}

type nativeAliasCatalog struct {
	mu      sync.Mutex
	expires time.Time
	routes  []jsplugin.Route
}

func nativeAliasRoutes(meta jsplugin.Meta) []jsplugin.Route {
	bindings := jsplugin.PluginRouteBindings(&jsplugin.LoadedPlugin{Meta: meta})
	routes := make([]jsplugin.Route, 0, len(bindings))
	for _, binding := range bindings {
		if NativeTaskAliasPathAllowed(binding.Route.Path) {
			routes = append(routes, binding.Route)
		}
	}
	return routes
}

// MayDispatchNativeTaskAlias is a structural prefilter only. Authorization and
// the actual version/model decision run after API key authentication.
func (s *GenerationService) MayDispatchNativeTaskAlias(ctx context.Context, method, path string, query url.Values) bool {
	if s == nil || s.plugins == nil || s.plugins.repo == nil || !NativeTaskAliasPathAllowed(path) {
		return false
	}
	if method == http.MethodGet && len(nativeAliasTaskIDs(path, query)) > 0 {
		// A still-owned task can use the route of a retired plugin revision.
		return true
	}
	cat := &s.nativeAliases
	cat.mu.Lock()
	defer cat.mu.Unlock()
	if time.Now().After(cat.expires) {
		loadCtx, cancel := context.WithTimeout(ctx, time.Second)
		versions, err := s.plugins.repo.List(loadCtx)
		cancel()
		cat.expires = time.Now().Add(2 * time.Second)
		cat.routes = nil
		if err == nil {
			for _, version := range versions {
				if !version.Capabilities.Task {
					continue
				}
				for _, route := range nativeAliasRoutes(version.Meta) {
					if version.Enabled || route.Type == jsplugin.RouteTypeDynamic {
						cat.routes = append(cat.routes, route)
					}
				}
			}
		}
	}
	_, _, ok := taskprotocol.MatchNativeRoute(cat.routes, method, path)
	return ok
}

func nativeAliasTaskID(path string) string {
	for _, part := range strings.Split(path, "/") {
		if generation.IsPublicTaskID(part) {
			return part
		}
	}
	return ""
}

func nativeAliasTaskIDs(path string, query url.Values) []string {
	var ids []string
	for _, part := range strings.Split(path, "/") {
		if generation.IsPublicTaskID(part) && !slices.Contains(ids, part) {
			ids = append(ids, part)
		}
	}
	if len(query) > 0 {
		keys := make([]string, 0, len(query))
		for k := range query {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			for _, v := range query[k] {
				for _, token := range strings.Split(v, ",") {
					token = strings.TrimSpace(token)
					if generation.IsPublicTaskID(token) && !slices.Contains(ids, token) {
						ids = append(ids, token)
						if len(ids) >= 16 {
							return ids
						}
					}
				}
			}
		}
	}
	return ids
}

func extractJSONPublicTaskIDs(body []byte) []string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return nil
	}
	var ids []string
	var walk func(res gjson.Result, depth int)
	walk = func(res gjson.Result, depth int) {
		if depth > 6 || len(ids) >= 16 {
			return
		}
		switch {
		case res.Type == gjson.String:
			if v := strings.TrimSpace(res.Str); generation.IsPublicTaskID(v) && !slices.Contains(ids, v) {
				ids = append(ids, v)
			}
		case res.IsObject() || res.IsArray():
			res.ForEach(func(_, value gjson.Result) bool {
				walk(value, depth+1)
				return len(ids) < 16
			})
		}
	}
	walk(gjson.ParseBytes(body), 0)
	return ids
}

type NativeTaskAliasMatch struct {
	PluginKey string
	VersionID int64
	Route     jsplugin.Route
	Params    map[string]string
}

// ResolveNativeTaskAlias picks from the authenticated group's bound accounts.
// A shared path with multiple claimants needs an explicit plugin prefix or
// unambiguous model. It must never silently pick a different paid protocol.
func (s *GenerationService) ResolveNativeTaskAlias(ctx context.Context, groupID *int64, platform, method, path, model string) (NativeTaskAliasMatch, bool, error) {
	if s == nil || s.accounts == nil || s.plugins == nil || groupID == nil || !NativeTaskAliasPathAllowed(path) {
		return NativeTaskAliasMatch{}, false, nil
	}
	accounts, err := s.listSchedulablePlatformAccounts(ctx, groupID, NormalizeOpenAICompatiblePlatform(platform))
	if err != nil {
		return NativeTaskAliasMatch{}, false, err
	}
	versions := make(map[int64]*NewAPIPluginVersion)
	var selected NativeTaskAliasMatch
	for i := range accounts {
		account := &accounts[i]
		id := account.NewAPIPluginVersionID()
		if id <= 0 || len(account.NewAPINativeRoutesBinding().Routes) == 0 {
			continue
		}
		version, seen := versions[id]
		if !seen {
			version, err = s.plugins.repo.Get(ctx, id)
			if err != nil {
				return NativeTaskAliasMatch{}, false, err
			}
			versions[id] = version
		}
		if version == nil || !version.Enabled || !version.Capabilities.Task {
			continue
		}
		route, params, ok := taskprotocol.MatchNativeRoute(nativeAliasRoutes(version.Meta), method, path)
		if !ok || model != "" && route.Type == jsplugin.RouteTypeSubmit && (!account.SupportsNewAPINativeRouteModel(version.Meta.Key, model) || len(route.Models) > 0 && !nativeVideoRouteModel(route.Models, model, account.GetMappedModel(model))) {
			continue
		}
		if selected.VersionID != 0 && selected.PluginKey != version.Meta.Key {
			return NativeTaskAliasMatch{}, false, ErrGenerationDecode
		}
		if selected.VersionID == 0 {
			selected = NativeTaskAliasMatch{PluginKey: version.Meta.Key, VersionID: id, Route: route, Params: params}
		}
	}
	return selected, selected.VersionID != 0, nil
}

// ResolveNativeTaskQuery uses the historical task's own manifest rather than
// requiring the original account to still be routable for a new request.
func (s *GenerationService) ResolveNativeTaskQuery(ctx context.Context, key *APIKey, pluginKey, path string, query url.Values) (*GenerationTask, NativeTaskAliasMatch, bool, error) {
	ids := nativeAliasTaskIDs(path, query)
	if len(ids) == 0 {
		return nil, NativeTaskAliasMatch{}, false, nil
	}
	var lastErr error
	for _, id := range ids {
		task, err := s.GetPluginTask(ctx, id, key)
		if err != nil {
			lastErr = err
			continue
		}
		version, err := s.GetPluginVersionMeta(ctx, task.PluginVersionID)
		if err != nil {
			return nil, NativeTaskAliasMatch{}, false, err
		}
		if version == nil || pluginKey != "" && version.Meta.Key != pluginKey {
			continue
		}
		routes := version.Meta.Routes
		if pluginKey == "" {
			routes = nativeAliasRoutes(version.Meta)
		}
		route, params, ok := taskprotocol.MatchNativeRoute(routes, http.MethodGet, path)
		if !ok || route.Type != jsplugin.RouteTypeQuery {
			continue
		}
		if route.TaskIDParam != "" && params[route.TaskIDParam] == "" && query != nil {
			if qval := strings.TrimSpace(query.Get(route.TaskIDParam)); qval != "" {
				params[route.TaskIDParam] = qval
			}
		}
		if taskprotocol.NativeRouteTaskID(route, params) != id {
			continue
		}
		return task, NativeTaskAliasMatch{PluginKey: version.Meta.Key, VersionID: version.ID, Route: route, Params: params}, true, nil
	}
	if lastErr != nil {
		return nil, NativeTaskAliasMatch{}, false, lastErr
	}
	return nil, NativeTaskAliasMatch{}, false, nil
}

// ResolveNativeTaskHistoricalBatchQuery resolves a dynamic batch-query route
// using an owned historical task's pinned plugin version so callers can still
// query previously accepted tasks after an account unbinds or disables a plugin.
func (s *GenerationService) ResolveNativeTaskHistoricalBatchQuery(ctx context.Context, key *APIKey, pluginKey, method, path string, body []byte) (NativeTaskAliasMatch, bool, error) {
	if s == nil || key == nil || pluginKey == "" && !NativeTaskAliasPathAllowed(path) {
		return NativeTaskAliasMatch{}, false, nil
	}
	for _, id := range extractJSONPublicTaskIDs(body) {
		task, err := s.GetPluginTask(ctx, id, key)
		if err != nil || task == nil {
			continue
		}
		version, err := s.GetPluginVersionMeta(ctx, task.PluginVersionID)
		if err != nil || version == nil || !version.Capabilities.Task {
			continue
		}
		if pluginKey != "" && version.Meta.Key != pluginKey {
			continue
		}
		routes := version.Meta.Routes
		if pluginKey == "" {
			routes = nativeAliasRoutes(version.Meta)
		}
		route, params, ok := taskprotocol.MatchNativeRoute(routes, method, path)
		if !ok || route.Type != jsplugin.RouteTypeDynamic {
			continue
		}
		return NativeTaskAliasMatch{PluginKey: version.Meta.Key, VersionID: version.ID, Route: route, Params: params}, true, nil
	}
	return NativeTaskAliasMatch{}, false, nil
}
