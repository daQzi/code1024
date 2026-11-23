package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
)

// VersionInfo resolves a pinned account revision which may no longer be the
// registry's default. It never evaluates source or returns account secrets.
func (s *NewAPIPluginService) VersionInfo(ctx context.Context, id int64) (*NewAPIPluginVersion, error) {
	if id <= 0 {
		return nil, pluginInputError("插件版本 ID 无效")
	}
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, newAPIProviderMemory); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	version, err := s.repo.Get(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errPluginSourceMissing
	}
	if err != nil {
		return nil, err
	}
	if version == nil || version.ID != id {
		return nil, errPluginSourceMissing
	}
	info := *version
	info.Source = ""
	info.Compatibility = AssessPluginHost(info.Meta, info.Capabilities)
	applySunoTaskCompatibility(&info)
	return &info, nil
}
