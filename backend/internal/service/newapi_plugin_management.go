package service

import (
	"context"
	"maps"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

// Called after management memory admission. The registry, connection, proxy
// and credential are fixed for the whole manual query, including retries.
func (s *NewAPIPluginService) savedPluginManagementAccount(ctx context.Context, id int64, allowLogin ...bool) (*Account, *NewAPIPluginVersion, string, error) {
	if s == nil || s.gateway == nil || s.gateway.accountRepo == nil {
		return nil, nil, "", pluginInputError("plugin account service unavailable")
	}
	account, err := s.gateway.accountRepo.GetByID(ctx, id)
	if err != nil || account == nil || account.ID != id || account.Type != AccountTypeAPIKey || account.IsShadow() || account.NewAPIPluginVersionID() <= 0 {
		return nil, nil, "", pluginInputError("请选择已保存且已绑定插件的普通 API Key 账号")
	}
	account, err = s.providerCredentialAccount(ctx, account)
	if err != nil {
		return nil, nil, "", pluginInputError("账号代理不可用")
	}
	pinned := *account
	pinned.Credentials = maps.Clone(account.Credentials)
	if account.Proxy != nil {
		proxy := *account.Proxy
		pinned.Proxy = &proxy
	}
	if account.ProxyID != nil {
		id := *account.ProxyID
		pinned.ProxyID = &id
	}
	version, err := s.repo.Get(ctx, pinned.NewAPIPluginVersionID())
	if err != nil || version == nil || version.ID != pinned.NewAPIPluginVersionID() || !version.Enabled || !AssessPluginHost(version.Meta, version.Capabilities).Runnable {
		return nil, nil, "", pluginInputError("该固定插件版本未启用或当前宿主不可用")
	}
	pinnedVersion := *version
	base, err := s.gateway.validateUpstreamBaseURL(pinned.GetCredential("base_url"))
	if err != nil {
		return nil, nil, "", pluginInputError("上游地址无效")
	}
	key := pinned.GetCredential("api_key")
	if _, credential := jsplugin.ParseCredential(key); (credential || key == "") && (len(allowLogin) == 0 || !allowLogin[0]) {
		return nil, nil, "", pluginInputError("插件管理查询目前需要静态 API Key")
	}
	return &pinned, &pinnedVersion, base, nil
}
