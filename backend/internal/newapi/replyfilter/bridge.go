// Package replyfilter retains the host import path while the reusable reply
// engine lives in the independent module. Authentication, execution budgets,
// billing, request ownership and audits are still supplied by the host.
package replyfilter

import (
	"context"

	engine "github.com/Wei-Shaw/sub2api/newapi-engine/replyfilter"
	"github.com/gin-gonic/gin"
)

const StageReply = engine.StageReply
const MaxBodyBytes = engine.MaxBodyBytes
const MaxInstances = engine.MaxInstances

type Instance = engine.Instance
type Record = engine.Record
type Failure = engine.Failure
type Writer = engine.Writer
type SocketFilter = engine.SocketFilter

var _ gin.ResponseWriter = (*Writer)(nil)

func New(ctx context.Context, writer gin.ResponseWriter, format string, instances []*Instance, onStop func()) (*Writer, context.Context, error) {
	return engine.New(ctx, writer, format, instances, onStop)
}

func NewSocket(ctx context.Context, format string, instances []*Instance, send func([]byte), onStop func()) (*SocketFilter, context.Context, error) {
	return engine.NewSocket(ctx, format, instances, send, onStop)
}

func Rewrite(ctx context.Context, format string, body []byte, instances []*Instance) ([]byte, []Record, *Failure, error) {
	return engine.Rewrite(ctx, format, body, instances)
}
