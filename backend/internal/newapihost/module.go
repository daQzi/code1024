// Package newapihost composes the standalone New API engine with Sub's
// existing account, encrypted credential, ledger and storage adapters.
package newapihost

import (
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/google/wire"
)

// Services is the production assembly entry. Existing handlers consume the
// host facades while execution, registry and query domains live in the engine.
// There is deliberately no native PluginManager here.
type Services struct {
	Plugins     *service.NewAPIPluginService
	Generations *service.GenerationService
	disabled    bool
	once        sync.Once
}

// Enabled reports whether the standalone New API module was started for this
// host instance. When false (cfg.NewAPIEngine.Enabled == false), Plugins and
// Generations are nil and native Sub2API / .s2plugin paths continue unaffected.
func (s *Services) Enabled() bool {
	return s != nil && !s.disabled && (s.Plugins != nil || s.Generations != nil)
}

func ProvideServices(cfg *config.Config, repo service.NewAPIPluginRepository, encryptor service.SecretEncryptor,
	gateway *service.OpenAIGatewayService, proxies service.ProxyRepository,
	tasks service.GenerationTaskRepository, settings service.SettingRepository,
	notifications *service.NotificationEmailService, accounts service.AccountRepository,
	cache *service.BillingCacheService, keys *service.APIKeyService,
) (*Services, error) {
	if cfg != nil && !cfg.NewAPIEngine.Enabled {
		return &Services{disabled: true}, nil
	}
	plugins, err := service.ProvideNewAPIPluginService(repo, encryptor, gateway, proxies, tasks, settings, notifications)
	if err != nil {
		return nil, err
	}
	host := service.NewGenerationHostService(tasks, plugins, accounts, gateway, cache, keys)
	executor := generation.NewExecutor(NewTaskExecutorPorts(tasks, host, accounts, plugins))
	if err := host.StartTaskExecutor(executor); err != nil {
		host.Stop()
		plugins.Stop()
		return nil, err
	}
	return &Services{Plugins: plugins, Generations: host}, nil
}

func (s *Services) Stop() {
	if s != nil {
		s.once.Do(func() {
			// Drain task/host maintenance before shutting down JS workers.
			s.Generations.Stop()
			s.Plugins.Stop()
		})
	}
}

func ProvidePlugins(host *Services) *service.NewAPIPluginService {
	if host == nil {
		return nil
	}
	return host.Plugins
}

func ProvideGenerations(host *Services) *service.GenerationService {
	if host == nil {
		return nil
	}
	return host.Generations
}

var ProviderSet = wire.NewSet(ProvideServices, ProvidePlugins, ProvideGenerations)
