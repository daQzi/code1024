package service

import (
	"context"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type nativeReplyAbortKey struct{}
type nativeReplySessionKey struct{}
type nativeReplyAbort struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// A local policy stop closes the actual upstream immediately, independently
// of the normal client-disconnect drain for late billing usage. Values added
// by the native gateway after reply admission must remain on the request.
type nativeReplyUpstreamContext struct {
	context.Context
	abort context.Context
}

func (c nativeReplyUpstreamContext) Deadline() (time.Time, bool) { return c.abort.Deadline() }
func (c nativeReplyUpstreamContext) Done() <-chan struct{}       { return c.abort.Done() }
func (c nativeReplyUpstreamContext) Err() error                  { return c.abort.Err() }
func (c nativeReplyUpstreamContext) AfterFunc(f func()) func() bool {
	return context.AfterFunc(c.abort, f)
}

func withNativeReplyUpstreamAbort(ctx context.Context) context.Context {
	if abort, ok := ctx.Value(nativeReplyAbortKey{}).(nativeReplyAbort); ok {
		return nativeReplyUpstreamContext{Context: context.WithoutCancel(ctx), abort: abort.ctx}
	}
	return ctx
}

// Reply observes the actual client protocol after the native gateway has
// finished its own conversion. It does not replace or re-run that converter.
// All ownership stays within the handler; only the existing bounded report
// executor may retain its independent, sanitized completion snapshot.
func (s *OpenAIGatewayService) forwardNativeWithReply(ctx context.Context, c *gin.Context, account *Account, body []byte, defaultMappedModel string, forward func(context.Context) (*OpenAIForwardResult, error)) (result *OpenAIForwardResult, retErr error) {
	model := resolveOpenAIForwardModel(account, gjson.GetBytes(body, "model").String(), defaultMappedModel)
	child, finish, err := beginNativeProtocolReply(ctx, c, account, model)
	if err != nil {
		return nil, err
	}
	defer finish(&retErr)
	return forward(child)
}

// All native gateways attach the same reply filter after their own model
// resolution and before transport. The filter sees the client protocol and
// cannot change the original upstream usage or open a second billing path.
// Its lifetime is synchronous: finish restores Gin state before returning.
func beginNativeProtocolReply(ctx context.Context, c *gin.Context, account *Account, upstreamModel string) (context.Context, func(*error), error) {
	noop := func(*error) {}
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	if pinned.plan == nil || account == nil || len(pinned.plan.replyItems(account.ID, requestPluginChannelType(account, false))) == 0 {
		return ctx, noop, nil
	}
	if pinned.plan.service == nil {
		return ctx, noop, writeProviderRequestPluginFailure(c, &RequestPluginFailure{Class: "unavailable"})
	}
	parent := ctx
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	abortCtx, abort := context.WithCancel(context.WithoutCancel(ctx))
	ctx = context.WithValue(ctx, nativeReplyAbortKey{}, nativeReplyAbort{ctx: abortCtx, cancel: abort})
	child, session, err := pinned.plan.service.beginRelayReply(ctx, c, account, upstreamModel, false)
	if err != nil {
		abort()
		release()
		return ctx, noop, writeProviderRequestPluginFailure(c, err)
	}
	if session == nil {
		abort()
		release()
		return parent, noop, nil
	}
	child = context.WithValue(child, nativeReplySessionKey{}, session)
	originalWriter, originalRequest := c.Writer, c.Request
	c.Writer, c.Request = session.writer, c.Request.WithContext(child)
	finish := func(retErr *error) {
		defer release()
		defer abort()
		defer func() { c.Writer, c.Request = originalWriter, originalRequest }()
		if err := session.finish(ctx, c); err != nil {
			*retErr = err
		}
	}
	return child, finish, nil
}

// Model fallback is resolved synchronously before any reply is written. Keep
// the hook identity aligned with that final upstream choice, not the first
// rejected candidate. No account or request is retained by the session.
func setNativeReplyUpstreamModel(ctx context.Context, model string) {
	if session, _ := ctx.Value(nativeReplySessionKey{}).(*providerReplySession); session != nil {
		session.upstreamModel = model
	}
}
