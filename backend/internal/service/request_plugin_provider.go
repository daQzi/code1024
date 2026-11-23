// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// Stage ordering and request summaries follow QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f, relay/requestplugin/{plan,relay}.go.
// Account placement, bounded subprocesses and audit storage are local host code.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/newapi/mediahost"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type requestPluginProviderBodyKey struct{}
type requestPluginProviderDryRunKey struct{}

var requestPluginMediaPlaceholder = regexp.MustCompile(`\[media:\d+\]`)

func (p *RequestPluginPlan) attemptItems(accountID int64, channelType int) []RequestPluginInstance {
	if p == nil {
		return nil
	}
	var items []RequestPluginInstance
	for _, item := range p.items {
		if p.itemMatchesChannel(item, accountID, channelType) {
			items = append(items, item)
		}
	}
	return items
}

func writeProviderRequestPluginFailure(c *gin.Context, err error) error {
	format := ""
	if pinned, _ := c.Request.Context().Value(requestPluginPlanKey{}).(requestPluginPlanContext); pinned.plan != nil {
		format = pinned.plan.call.Format
	}
	return writeRequestPluginFailure(c, err, format)
}

func writeRequestPluginFailure(c *gin.Context, err error, format string) error {
	var failure *RequestPluginFailure
	if !errors.As(err, &failure) {
		return err
	}
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	status, code := http.StatusServiceUnavailable, "request_processing_failed"
	if failure.Rejected {
		status, code = http.StatusForbidden, "request_processing_rejected"
	}
	if failure.Class == "capacity" {
		c.Header("Retry-After", "1")
	}
	payload := gin.H{"error": gin.H{"type": "request_processing_error", "code": code, "message": failure.Error()}}
	switch format {
	case "claude":
		payload["type"] = "error"
	case "gemini":
		state := "UNAVAILABLE"
		if failure.Rejected {
			state = "PERMISSION_DENIED"
		}
		payload = gin.H{"error": gin.H{"code": status, "status": state, "message": failure.Error()}}
	}
	c.JSON(status, payload)
	return failure
}

// Run before leasing the Provider worker: Transform cannot nest a second pool
// lease inside convertRequest/prepareRequest, including on credential renewal.
func (s *NewAPIPluginService) providerRequestPluginStages(ctx context.Context, account *Account, path, model string, body []byte, route contract.AdvancedCustomRoute) (RequestPluginOutcome, error) {
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	attempt := 0
	if pinned.plan != nil {
		attempt = pinned.plan.nextAttempt
		pinned.plan.nextAttempt++
	}
	return s.relayRequestPluginStages(ctx, account, path, model, body, route, requestPluginStageRun{provider: true, attempt: attempt, stages: []string{"request", "ingress", "outbound", "egress"}})
}

type requestPluginStageRun struct {
	provider       bool
	attempt        int
	stages         []string
	protected      func(string) bool
	passthrough    bool
	unpriced       bool // Task late ingress, before decoding and estimating usage.
	format         string
	bodyGuard      func([]byte, []byte) bool
	videoForm      bool
	multipart      *RequestPluginMultipart
	mediaFiles     []mediahost.MultipartFile
	deferReasoning bool // A native cross-protocol gateway renders at its real target.
}

func (s *NewAPIPluginService) relayRequestPluginStages(ctx context.Context, account *Account, path, model string, body []byte, route contract.AdvancedCustomRoute, run requestPluginStageRun) (out RequestPluginOutcome, retErr error) {
	out = RequestPluginOutcome{Body: body, Records: []RequestPluginRecord{}}
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	plan := pinned.plan
	if plan == nil {
		return out, nil
	}
	attempt := run.attempt
	items := plan.attemptItems(account.ID, requestPluginChannelType(account, run.provider))
	items = slices.DeleteFunc(items, func(item RequestPluginInstance) bool {
		return !slices.ContainsFunc(item.Stages, func(stage string) bool {
			return slices.Contains(run.stages, stage) && (stage != "ingress" || plan.lateIngressItem(item))
		})
	})
	if len(items) == 0 {
		return out, nil // Reply-only rules still count each actual Provider attempt.
	}
	dryStage, dryRun := ctx.Value(requestPluginProviderDryRunKey{}).(string)
	parentCtx := ctx
	defer func() {
		if len(out.Records) == 0 && retErr != nil {
			class := "execution"
			var failure *RequestPluginFailure
			if errors.As(retErr, &failure) {
				class = failure.Class
			}
			out.Records = append(out.Records, RequestPluginRecord{InstanceID: items[0].ID, VersionID: items[0].PluginVersionID, Stage: items[0].Stages[0], Action: "error", ErrorClass: class, AccountID: account.ID, Attempt: attempt})
		}
		if !dryRun {
			plan.appendReportRecords(out.Records)
			auditCtx, cancel := context.WithTimeout(context.WithoutCancel(parentCtx), 2*time.Second)
			err := s.RecordRequestPluginRun(auditCtx, plan.call, out, false)
			cancel()
			if err != nil {
				retErr = &RequestPluginFailure{Class: "audit"}
			}
		}
		if retErr == nil {
			retErr = parentCtx.Err()
		}
		if retErr == nil {
			retErr = pkghttputil.ReserveRequestBodyMemory(parentCtx, int64(len(out.Body))+(64<<10))
			if retErr != nil {
				retErr = &RequestPluginFailure{Class: "capacity"}
			}
		}
	}()
	clientBody, _ := ctx.Value(requestPluginProviderBodyKey{}).([]byte)
	if clientBody == nil {
		return out, &RequestPluginFailure{Class: "identity"}
	}
	if len(body) > MaxRequestPluginBody || len(clientBody) > MaxRequestPluginBody ||
		!gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() ||
		!gjson.ValidBytes(clientBody) || !gjson.ParseBytes(clientBody).IsObject() ||
		!newAPIProviderUnambiguousJSON(gjson.ParseBytes(body), 0) || !newAPIProviderUnambiguousJSON(gjson.ParseBytes(clientBody), 0) || !requestPluginUnambiguousReasoning(body) {
		return out, &RequestPluginFailure{Class: "unsupported_body"}
	}
	// Keep stage scratch allocations separate from the resulting request body.
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, int64(len(body)+len(clientBody))*8+(8<<20)); err != nil {
		return out, &RequestPluginFailure{Class: "capacity"}
	}
	format := newAPIProviderFormat(path)
	if run.format != "" {
		format = run.format
	}
	if format == "claude" && (!requestpatch.UnambiguousClaudeReasoning(body) || !requestpatch.UnambiguousClaudeReasoning(clientBody)) {
		return out, &RequestPluginFailure{Class: "unsupported_body"}
	}
	if format == "gemini" && (!requestpatch.UnambiguousGeminiReasoning(body) || !requestpatch.UnambiguousGeminiReasoning(clientBody)) {
		return out, &RequestPluginFailure{Class: "unsupported_body"}
	}
	initialEffort := gjson.GetBytes(clientBody, "reasoning_effort").String()
	initialReasoning := initialEffort != ""
	switch format {
	case "openai_responses":
		initialEffort = gjson.GetBytes(clientBody, "reasoning.effort").String()
		initialReasoning = initialEffort != ""
	case "claude":
		initialEffort, initialReasoning = requestpatch.ClaudeReasoningEffort(clientBody)
	case "gemini":
		initialEffort, initialReasoning = requestpatch.GeminiReasoningEffort(clientBody)
		if plan.call.Format == "claude" {
			initialEffort, initialReasoning = requestpatch.ClaudeReasoningEffort(clientBody)
		}
	}
	if initialEffort == "" {
		if intent, ok := modelmodifier.Intent(ctx); ok {
			initialEffort = (modelmodifier.Spec{Thinking: true, Intent: intent}).Effort()
			initialReasoning = initialEffort != ""
		}
	}
	summary := summarizeRequestPluginBody(clientBody)
	summary.ReasoningEffort = initialEffort
	headers := http.Header{}
	// New API runs a channel's late ingress on the mapped upstream body,
	// after request/reasoning and before outbound. It repeats per attempt;
	// Instances already run at the client boundary are skipped. A channel
	// scope that could not match there is evaluated on each selected attempt.
	for _, stage := range run.stages {
		if dryRun && stage != dryStage {
			continue
		}
		if stage == "egress" && (path == newAPIProviderChat || path == newAPIProviderResponses) && !dryRun && !run.passthrough {
			// The host's policy has the last word. Provider conversion and
			// prepareRequest still happen later, as in the pinned contract.
			var err error
			out.Body, err = s.gateway.applyOpenAIFastPolicyToBody(ctx, account, model, out.Body)
			if err != nil {
				return out, err
			}
			if path == newAPIProviderChat && gjson.GetBytes(out.Body, "stream").Bool() {
				out.Body, err = ensureOpenAIChatStreamUsage(out.Body)
				if err != nil {
					return out, err
				}
			}
		}
		stageCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		for _, instance := range items {
			if !slices.Contains(instance.Stages, stage) || stage == "ingress" && !plan.lateIngressItem(instance) {
				continue
			}
			started := time.Now()
			record := RequestPluginRecord{InstanceID: instance.ID, VersionID: instance.PluginVersionID, Stage: stage, Action: "continue", AccountID: account.ID, Attempt: attempt}
			if run.passthrough && (stage == "ingress" || stage == "outbound") {
				record.Action = "unchecked"
				out.Records = append(out.Records, record)
				continue
			}
			stageBody := out.Body
			if stage == "request" {
				stageBody = clientBody
			}
			var source *mediahost.Source
			var sourceErr error
			if len(run.mediaFiles) > 0 {
				source, sourceErr = mediahost.NewWithMultipartFiles(stageBody, &plan.mediaText, run.mediaFiles)
			} else if run.multipart != nil {
				source, sourceErr = run.multipart.MediaSource(stageBody, &plan.mediaText)
			} else if run.unpriced && format == "task" {
				source, sourceErr = requestPluginVideoSource(ctx, stageBody, &plan.mediaText)
			} else {
				source, sourceErr = mediahost.NewWithCache(stageBody, &plan.mediaText)
			}
			var result jsplugin.TransformResult
			class := "execution"
			var err error
			if sourceErr != nil {
				err = errors.New("request projection exceeds limit")
			} else {
				err = s.pool.With(stageCtx, instance.Version.Source, func(client *runner.Client) error {
					request := jsplugin.TransformRequest{Body: source.View(), Media: source.Projection()}
					if stage == "request" {
						request.Summary = &summary
					} else {
						request.Headers = pluginResponseHeaders(headers)
					}
					values, err := s.callRequestPluginTransform(stageCtx, client, instance.NetworkEnabled, jsplugin.TransformContext{
						Stage: stage, Format: format, Path: plan.call.Path, Model: plan.call.Model, UpstreamModel: model,
						Group: plan.call.Group, Channel: &jsplugin.TransformChannel{ID: int(account.ID), Type: requestPluginChannelType(account, run.provider)}, Attempt: attempt,
						User: jsplugin.TransformUser{ID: int(plan.call.UserID)}, Token: &jsplugin.TransformToken{ID: int(plan.call.TokenID), Name: plan.call.TokenName},
						Instance: jsplugin.TransformInstance{ID: strconv.FormatInt(instance.ID, 10), Name: instance.Name}, Config: instance.Config, Media: source,
					}, request)
					if errors.Is(err, pkghttputil.ErrRequestBodyMemoryBudget) || errors.Is(err, mediahost.ErrMemoryCapacity) {
						class = "capacity"
					}
					if err != nil || len(values) != 1 {
						return errors.New("request stage execution failed")
					}
					return json.Unmarshal(values[0], &result)
				})
			}
			if err == nil && result.Action == "reject" {
				record.Action, out.Rejected = "reject", true
				class = "rejected"
			} else if err == nil {
				class = "invalid_effect"
				var effects []requestpatch.Effect
				allowed := []string{}
				switch stage {
				case "request":
					allowed = []string{requestpatch.EffectReasoningSet}
				case "ingress":
					allowed = []string{requestpatch.EffectJSONPatch, requestpatch.EffectMediaText}
				case "outbound":
					allowed = []string{requestpatch.EffectJSONPatch, requestpatch.EffectHeaderPatch, requestpatch.EffectMediaText, requestpatch.EffectMediaReencode}
				}
				if len(result.Effects) > 0 {
					effects, err = requestpatch.DecodeTransformEffects(result.Effects, allowed...)
				}
				if err == nil && stage == "request" {
					patched := out.Body
					reasoningSet := false
					appliedEffort := ""
					for _, effect := range effects {
						if !requestPluginTextGenerationPath(path) {
							err = errors.New("this endpoint does not accept text generation reasoning effects")
							break
						}
						if !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, effect.Effort) {
							err = errors.New("invalid reasoning effort")
							break
						}
						if !initialReasoning || effect.Override {
							appliedEffort = effect.Effort
							if run.deferReasoning {
								reasoningSet = true
								continue
							}
							if format == "gemini" {
								patched, err = requestpatch.SetGeminiReasoning(patched, model, effect.Effort)
								if err != nil {
									break
								}
								reasoningSet = true
								continue
							}
							if format == "claude" {
								patched, err = requestpatch.SetClaudeReasoning(patched, model, effect.Effort)
								if err != nil {
									break
								}
								reasoningSet = true
								continue
							}
							field := "reasoning_effort"
							if format == "openai_responses" {
								field = "reasoning.effort"
							}
							patched, err = sjson.SetBytes(patched, field, effect.Effort)
							if err == nil && format != "openai_responses" {
								for _, old := range []string{"reasoning.effort", "reasoning.enabled", "reasoning.max_tokens"} {
									patched, err = sjson.DeleteBytes(patched, old)
									if err != nil {
										break
									}
								}
							}
							if err != nil {
								break
							}
							reasoningSet = true
						}
					}
					if err == nil {
						out.Body = patched
						out.ReasoningSet = out.ReasoningSet || reasoningSet
						if reasoningSet {
							out.ReasoningEffort = appliedEffort
						}
					}
				} else if err == nil && (stage == "ingress" || stage == "outbound") {
					if requestPluginEffectHasMediaPlaceholder(effects) {
						err = errors.New("media placeholders cannot be inserted")
					} else {
						var patched []byte
						var nextHeaders http.Header
						var prepared *mediahost.PreparedEffects
						prepared, err = source.PrepareEffects(stageCtx, effects)
						if err == nil && run.unpriced && format == "task" {
							err = checkVideoPolicyReferenceEffects(prepared.Effects)
						}
						if err == nil && run.videoForm {
							err = checkGenerationMultipartPolicyEffects(out.Body, prepared.Effects)
						}
						if err == nil && run.multipart != nil {
							err = run.multipart.CheckEffects(prepared.Effects, true)
						}
						if err == nil {
							patched, nextHeaders, err = requestpatch.ApplyWithLimit(out.Body, headers, prepared.Effects, func(name string) bool {
								return newAPIProviderProtectedHeader(route, name) || run.protected != nil && run.protected(name)
							}, MaxRequestPluginBody)
						}
						protectedChanged := !sameNewAPIProviderBillingFields(out.Body, patched)
						if run.unpriced {
							protectedChanged = string(requestPluginRoutingFields(out.Body)) != string(requestPluginRoutingFields(patched))
						}
						if run.bodyGuard != nil && !run.bodyGuard(out.Body, patched) {
							protectedChanged = true
						}
						if err == nil && run.multipart != nil && run.multipart.CheckView(out.Body, patched, true) != nil {
							protectedChanged = true
						}
						if err == nil && (!newAPIProviderUnambiguousJSON(gjson.ParseBytes(patched), 0) || !gjson.ParseBytes(patched).IsObject() || protectedChanged || !boundedRequestPluginHeaders(nextHeaders)) {
							err, class = errors.New("protected request member changed"), "guard"
						}
						if err == nil {
							out.Body, headers = patched, nextHeaders
							prepared.Commit()
							record.Media = prepared.Media
						}
					}
				}
				if err == nil {
					record.Effects = len(effects)
				}
			}
			if source != nil {
				record.Attachments, record.MoreAttachments = source.Records()
			}
			if errors.Is(err, pkghttputil.ErrRequestBodyMemoryBudget) || errors.Is(err, mediahost.ErrMemoryCapacity) {
				class = "capacity"
			}
			record.DurationMS, record.Findings = time.Since(started).Milliseconds(), len(result.Findings)
			if err != nil {
				record.Action, record.ErrorClass = "error", class
				if instance.Version.Meta.Optional && ctx.Err() == nil {
					record.Action = "skipped"
				}
			}
			out.Records = append(out.Records, record)
			if out.Rejected || err != nil && record.Action != "skipped" {
				cancel()
				return out, &RequestPluginFailure{Rejected: out.Rejected, Class: class}
			}
		}
		cancel()
	}
	out.Headers = pluginResponseHeaders(headers)
	return out, nil
}

func requestPluginTextGenerationPath(path string) bool {
	return path == newAPIProviderChat || path == newAPIProviderResponses || path == "/v1/messages" || strings.HasPrefix(path, "/v1beta/models/") && strings.HasSuffix(path, ":generateContent")
}

func requestPluginEffectHasMediaPlaceholder(effects []requestpatch.Effect) bool {
	for _, effect := range effects {
		for _, op := range effect.Ops {
			if op.Value != nil && requestPluginJSONHasMediaPlaceholder(gjson.ParseBytes(op.Value), 0) {
				return true
			}
		}
	}
	return false
}

func requestPluginJSONHasMediaPlaceholder(value gjson.Result, depth int) bool {
	if depth > 128 {
		return true
	}
	if value.Type == gjson.String {
		return requestPluginMediaPlaceholder.MatchString(value.Str)
	}
	found := false
	if value.IsObject() || value.IsArray() {
		value.ForEach(func(_, child gjson.Result) bool {
			found = requestPluginJSONHasMediaPlaceholder(child, depth+1)
			return !found
		})
	}
	return found
}

func boundedRequestPluginHeaders(headers http.Header) bool {
	if len(headers) > 64 {
		return false
	}
	size := 0
	for name, values := range headers {
		size += len(name)
		for _, value := range values {
			size += len(value)
		}
		if size > 64<<10 {
			return false
		}
	}
	return true
}

// Do not add a canonical effort alongside a case alias a vendor may decode as
// the same member. This check applies only when an account policy is matched.
func requestPluginUnambiguousReasoning(body []byte) bool {
	valid := true
	gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
		switch strings.ToLower(key.Str) {
		case "reasoning_effort", "reasoning":
			valid = key.Str == strings.ToLower(key.Str)
			if valid && key.Str == "reasoning" {
				value.ForEach(func(k, _ gjson.Result) bool {
					if strings.EqualFold(k.Str, "effort") && k.Str != "effort" {
						valid = false
					}
					return valid
				})
			}
		}
		return valid
	})
	return valid
}

func summarizeRequestPluginBody(body []byte) jsplugin.TransformSummary {
	summary := jsplugin.TransformSummary{}
	root := gjson.ParseBytes(body)
	messages := root.Get("messages")
	if !messages.Exists() {
		messages = root.Get("input")
	}
	gemini := false
	if !messages.Exists() {
		messages, gemini = root.Get("contents"), true
	}
	if messages.Type == gjson.String {
		summary.MessageCount = 1
		summary.LastUserTextLength = utf8.RuneCountInString(messages.Str)
		summary.TotalTextLength = summary.LastUserTextLength
	} else {
		messages.ForEach(func(_, message gjson.Result) bool {
			summary.MessageCount++
			length := 0
			content := message.Get("content")
			if gemini {
				content = message.Get("parts")
			}
			if content.Type == gjson.String {
				length = utf8.RuneCountInString(content.Str)
			} else {
				content.ForEach(func(_, part gjson.Result) bool {
					length += utf8.RuneCountInString(part.Get("text").Str)
					summary.HasImages = summary.HasImages || slices.Contains([]string{"image_url", "input_image", "image"}, part.Get("type").Str)
					if gemini {
						for _, path := range []string{"inlineData.mimeType", "inline_data.mime_type", "fileData.mimeType", "file_data.mime_type"} {
							summary.HasImages = summary.HasImages || strings.HasPrefix(part.Get(path).String(), "image/")
						}
					}
					return true
				})
			}
			summary.TotalTextLength += length
			if message.Get("role").Str == "user" {
				summary.LastUserTextLength = length
			}
			return true
		})
	}
	summary.TotalTextLength += utf8.RuneCountInString(root.Get("instructions").Str)
	if gemini {
		for _, path := range []string{"systemInstruction.parts", "system_instruction.parts"} {
			root.Get(path).ForEach(func(_, part gjson.Result) bool {
				summary.TotalTextLength += utf8.RuneCountInString(part.Get("text").String())
				return true
			})
		}
	}
	root.Get("tools").ForEach(func(_, _ gjson.Result) bool { summary.ToolCount++; return true })
	return summary
}
