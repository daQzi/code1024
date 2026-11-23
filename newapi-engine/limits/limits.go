// Package limits contains immutable limits for the pinned New API contract.
// These are independent from per-request Sub2API ingress and billing limits.
package limits

const (
	PluginMemoryLimitMB      = 64
	MaxRequestBodyMB         = 32
	StreamScannerMaxBufferMB = 1
	MaxTaskDurationSeconds   = 3600
	ChannelTypeNewAPI        = 60
	ChannelTypeTaskPlugin    = 61
)
