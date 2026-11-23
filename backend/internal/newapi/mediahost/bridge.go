// Package mediahost connects the standalone engine's media projection and
// codecs to Sub2API's existing shared ingress-memory budget. It owns no media
// parsing rules, file identities, task state or plugin runtime.
package mediahost

import (
	"context"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	core "github.com/Wei-Shaw/sub2api/newapi-engine/mediahost"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
)

const MaxBody = core.MaxBody

type Source = core.Source
type TextCache = core.TextCache
type Record = core.Record
type Change = core.Change
type MediaForm = core.MediaForm
type PreparedEffects = core.PreparedEffects
type MultipartFile = core.MultipartFile

var ErrMemoryCapacity = core.ErrMemoryCapacity

func reserveMemory(ctx context.Context, bytes int64) (context.Context, func(), error) {
	child, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	if err := pkghttputil.ReserveRequestBodyMemory(child, bytes); err != nil {
		release()
		return ctx, func() {}, err
	}
	return child, release, nil
}

func New(body []byte) (*Source, error) {
	return core.New(body, core.WithMemoryAdmission(reserveMemory))
}

func NewWithCache(body []byte, cache *TextCache) (*Source, error) {
	return core.NewWithCache(body, cache, core.WithMemoryAdmission(reserveMemory))
}

func NewWithMultipartFiles(body []byte, cache *TextCache, files []MultipartFile) (*Source, error) {
	return core.NewWithMultipartFiles(body, cache, files, core.WithMemoryAdmission(reserveMemory))
}

func NewWithFileReferences(ctx context.Context, body []byte, cache *TextCache, open pluginTask.OpenFile) (*Source, error) {
	return core.NewWithFileReferences(ctx, body, cache, open, core.WithMemoryAdmission(reserveMemory))
}
