package logbridge

import (
	"context"
	"fmt"
	"log/slog"
)

// LogDebug checks the level before formatting potentially expensive messages.
// Console logging remains disabled unless debug is explicitly enabled by host.
func LogDebug(ctx context.Context, format string, args ...any) {
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		slog.DebugContext(ctx, fmt.Sprintf(format, args...), "component", "newapi_plugin")
	}
}
