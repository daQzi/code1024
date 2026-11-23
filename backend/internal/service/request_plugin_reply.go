// SPDX-License-Identifier: AGPL-3.0-or-later
// Reply placement follows QuantumNous/new-api relay/requestplugin/reply.go at
// 48f9df96ea7c68eae04a0f3687869f232f48929f. Audit and account ownership are local.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/replyfilter"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/gin-gonic/gin"
)

// The reservation covers held text for 16 instances, a 4 MiB raw event queue
// and its parsed copies, released text, RPC results and a 1 MiB response body.
// It is admission accounting against shared ingress capacity, not an RSS cap.
const providerReplyMemory = 64 << 20

func (s *NewAPIPluginService) ReplyAuditFailures() uint64 {
	return s.replyAuditFailures.Load()
}

type providerReplySession struct {
	service       *NewAPIPluginService
	plan          *RequestPluginPlan
	items         []RequestPluginInstance
	accountID     int64
	attempt       int
	upstreamModel string
	admitted      bool
	writer        *replyfilter.Writer
}

func (s *NewAPIPluginService) replyInstances(plan *RequestPluginPlan, items []RequestPluginInstance, accountID int64, upstreamModel string, attempt func() int) []*replyfilter.Instance {
	return s.replyInstancesForChannel(plan, items, accountID, 61, func() string { return upstreamModel }, attempt)
}

func (s *NewAPIPluginService) replyInstancesForChannel(plan *RequestPluginPlan, items []RequestPluginInstance, accountID int64, channelType int, upstreamModel func() string, attempt func() int) []*replyfilter.Instance {
	instances := make([]*replyfilter.Instance, 0, len(items))
	for _, item := range items {
		instances = append(instances, &replyfilter.Instance{ID: strconv.FormatInt(item.ID, 10), Name: item.Name, Optional: item.Version.Meta.Optional,
			Call: func(ctx context.Context, stream bool, outputs []jsplugin.ReplyOutput) (result jsplugin.ReplyResult, err error) {
				err = s.pool.With(ctx, item.Version.Source, func(client *runner.Client) error {
					values, callErr := s.callRequestPluginStage(ctx, client, item.ReplyNetworkEnabled, jsplugin.TransformContext{
						Stage: "reply", Format: plan.call.Format, Path: plan.call.Path, Model: plan.call.Model, UpstreamModel: upstreamModel(),
						Group: plan.call.Group, Channel: &jsplugin.TransformChannel{ID: int(accountID), Type: channelType}, Attempt: attempt(),
						User: jsplugin.TransformUser{ID: int(plan.call.UserID)}, Token: &jsplugin.TransformToken{ID: int(plan.call.TokenID), Name: plan.call.TokenName},
						Instance: jsplugin.TransformInstance{ID: strconv.FormatInt(item.ID, 10), Name: item.Name}, Config: item.Config,
					}, "CallReply", stream, outputs)
					if callErr != nil || len(values) != 1 {
						return errors.New("reply processing execution failed")
					}
					return json.Unmarshal(values[0], &result)
				})
				return result, err
			}})
	}
	return instances
}

// beginProviderReply runs after Provider preparation and before the first
// upstream HTTP request. Only immutable, request-pinned policy is consulted.
// Finish must run while the handler still owns c, before the memory scope ends.
func (s *NewAPIPluginService) beginProviderReply(ctx context.Context, c *gin.Context, account *Account, upstreamModel string) (context.Context, *providerReplySession, error) {
	return s.beginRelayReply(ctx, c, account, upstreamModel, true)
}

func (p *RequestPluginPlan) replyItems(accountID int64, channelType int) []RequestPluginInstance {
	if p == nil {
		return nil
	}
	var items []RequestPluginInstance
	for _, item := range p.items {
		if slices.Contains(item.Stages, "reply") && p.itemMatchesChannel(item, accountID, channelType) {
			items = append(items, item)
		}
	}
	return items
}

func (s *NewAPIPluginService) beginRelayReply(ctx context.Context, c *gin.Context, account *Account, upstreamModel string, provider bool) (context.Context, *providerReplySession, error) {
	if c.Request != nil && (c.Request.URL.Path == newAPIProviderEmbeddings || isNativeImagesPluginPath(c.Request.URL.Path)) {
		// Match the pinned baseline's replyEndpoint: vectors are not answers
		// and must never be sent to a text redaction hook.
		return ctx, nil, nil
	}
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	items := pinned.plan.replyItems(account.ID, requestPluginChannelType(account, provider))
	if len(items) == 0 {
		return ctx, nil, nil
	}
	session := &providerReplySession{service: s, plan: pinned.plan, items: items, accountID: account.ID, attempt: max(0, pinned.plan.nextAttempt-1), upstreamModel: upstreamModel}
	class := ""
	if len(items) > replyfilter.MaxInstances {
		class = "limit"
	} else if err := pkghttputil.ReserveRequestBodyMemory(ctx, providerReplyMemory); err != nil {
		class = "capacity"
	}
	if class != "" {
		out := RequestPluginOutcome{Records: session.records("error", class)}
		if err := session.audit(ctx, out); err != nil {
			class = "audit"
		}
		return ctx, nil, &RequestPluginFailure{Class: class}
	}
	w, child, err := replyfilter.New(ctx, c.Writer, pinned.plan.call.Format, s.replyInstancesForChannel(pinned.plan, items, account.ID, requestPluginChannelType(account, provider), func() string { return session.upstreamModel }, func() int { return session.attempt }), func() {
		if abort, ok := ctx.Value(nativeReplyAbortKey{}).(nativeReplyAbort); ok {
			abort.cancel()
		}
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	})
	if err != nil {
		return ctx, nil, &RequestPluginFailure{Class: "reply_setup"}
	}
	session.writer = w
	if err := session.admit(ctx); err != nil {
		w.Finish()
		return ctx, nil, err
	}
	return child, session, nil
}

func (s *providerReplySession) records(action, class string) []RequestPluginRecord {
	records := make([]RequestPluginRecord, 0, len(s.items))
	for _, item := range s.items {
		records = append(records, RequestPluginRecord{InstanceID: item.ID, VersionID: item.PluginVersionID, Stage: "reply", Action: action, ErrorClass: class, AccountID: s.accountID, Attempt: s.attempt})
	}
	return records
}

func (s *providerReplySession) audit(ctx context.Context, out RequestPluginOutcome) error {
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	return s.service.RecordRequestPluginRun(auditCtx, s.plan.call, out, false)
}

// Credential renewal happens before any response bytes. Close the old
// attempt as unchecked and durably admit the next one before replaying HTTP.
func (s *providerReplySession) admit(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var records []RequestPluginRecord
	if s.admitted {
		records = s.records("unchecked", "")
	}
	previous := s.attempt
	s.attempt = max(0, s.plan.nextAttempt-1)
	records = append(records, s.records("started", "")...)
	if err := s.audit(ctx, RequestPluginOutcome{Records: records}); err != nil {
		s.attempt = previous
		return &RequestPluginFailure{Class: "audit"}
	}
	s.admitted = true
	return nil
}

func (s *providerReplySession) finish(ctx context.Context, c *gin.Context) error {
	if s == nil {
		return nil
	}
	records, stopped := s.writer.Finish()
	out := RequestPluginOutcome{Records: s.records("unchecked", "")}
	var failure *RequestPluginFailure
	for i, item := range s.items {
		for _, record := range records {
			if record.ID != strconv.FormatInt(item.ID, 10) {
				continue
			}
			out.Records[i].Action, out.Records[i].ErrorClass = record.Action, record.ErrorClass
			out.Records[i].Effects, out.Records[i].Findings, out.Records[i].DurationMS = record.Masked, record.Findings, record.DurationMS
			if stopped && record.Action == "reject" {
				failure = &RequestPluginFailure{Rejected: true, Class: "rejected"}
			}
		}
	}
	if stopped && failure == nil {
		failure = &RequestPluginFailure{Class: "reply_execution"}
	}
	s.plan.appendReportRecords(out.Records)
	if err := s.audit(ctx, out); err != nil {
		// Once response bytes were sent they cannot be recalled. A durable
		// started record remains, and an exposed counter makes failed terminal
		// audit observable without flooding logs or storing answer contents.
		count := s.service.replyAuditFailures.Add(1)
		if count == 1 || count%64 == 0 {
			slog.Warn("request_plugin_reply_terminal_audit_failed", "failures", count)
		}
		failure = &RequestPluginFailure{Class: "audit"}
	}
	if failure != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		return failure
	}
	return s.writer.Err()
}

func (s *NewAPIPluginService) dryRunReply(ctx context.Context, v *NewAPIPluginVersion, config, body json.RawMessage, call RequestPluginCall) (RequestPluginOutcome, error) {
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, providerReplyMemory); err != nil {
		return RequestPluginOutcome{}, &RequestPluginFailure{Class: "capacity"}
	}
	if len(body) > MaxRequestPluginBody || !json.Valid(body) {
		return RequestPluginOutcome{}, pluginInputError("回复试运行需要不超过 1 MiB 的 JSON 响应正文")
	}
	item := RequestPluginInstance{ID: 0, PluginVersionID: v.ID, Version: v, Config: config, Name: "Dry run"}
	plan := &RequestPluginPlan{call: call}
	answer, records, failure, err := replyfilter.Rewrite(ctx, call.Format, body, s.replyInstances(plan, []RequestPluginInstance{item}, 0, call.Model, func() int { return 0 }))
	out := RequestPluginOutcome{Body: answer, Records: []RequestPluginRecord{}}
	for _, r := range records {
		out.Records = append(out.Records, RequestPluginRecord{VersionID: v.ID, Stage: "reply", Action: r.Action, ErrorClass: r.ErrorClass, Effects: r.Masked, Findings: r.Findings, DurationMS: r.DurationMS})
	}
	if failure != nil {
		return out, &RequestPluginFailure{Rejected: failure.StatusCode == http.StatusForbidden, Class: "reply_execution"}
	}
	return out, err
}
