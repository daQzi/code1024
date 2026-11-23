package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
)

var ErrGenerationArtifactBusy = infraerrors.New(503, "VIDEO_CONTENT_BUSY", "Video content capacity is temporarily full; retry later")
var generationEmbeddedSlots = make(chan struct{}, 2)

type generationEmbeddedBody struct {
	io.Reader
	release func()
	once    sync.Once
}

func (b *generationEmbeddedBody) Close() error { b.once.Do(b.release); return nil }

// Embedded JSON is held while the decoded stream is served, as in the pinned
// host. Reserve before io.ReadAll: its growth, JSON string copy and possible
// unescaping are bounded separately from the decoded media (never allocated).
// Two fixed slots bound this path even when ingress memory accounting is off.
func generationEmbeddedResponse(ctx context.Context, upstream *http.Response, descriptor *pluginTask.Request, method string) (*http.Response, error) {
	defer func() { _ = upstream.Body.Close() }()
	if upstream.StatusCode != http.StatusOK {
		return nil, errors.New("embedded artifact upstream did not return HTTP 200")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case generationEmbeddedSlots <- struct{}{}:
	default:
		return nil, ErrGenerationArtifactBusy
	}
	budgetCtx, releaseBudget := pkghttputil.WithRequestBodyMemoryScope(ctx)
	release := func() { releaseBudget(); <-generationEmbeddedSlots }
	if err := pkghttputil.ReserveRequestBodyMemory(budgetCtx, pluginTask.MaxEmbeddedJSONBytes*5+1<<20); err != nil {
		release()
		return nil, err
	}
	if upstream.ContentLength > pluginTask.MaxEmbeddedJSONBytes {
		release()
		return nil, errors.New("embedded artifact JSON exceeds size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(generationUploadReader{ctx: ctx, Reader: upstream.Body}, pluginTask.MaxEmbeddedJSONBytes+1))
	if err != nil {
		release()
		return nil, err
	}
	reader, size, err := pluginTask.DecodeEmbeddedArtifact(raw, descriptor.ContentPath)
	if err != nil {
		release()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	kind := descriptor.MimeType
	if kind == "" {
		kind = "video/mp4"
	}
	body := io.ReadCloser(&generationEmbeddedBody{Reader: generationUploadReader{ctx: ctx, Reader: reader}, release: release})
	if method == http.MethodHead {
		release()
		body = http.NoBody
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {kind}, "Content-Length": {strconv.FormatInt(size, 10)}, "Accept-Ranges": {"none"}}, Body: body, ContentLength: size}, nil
}
