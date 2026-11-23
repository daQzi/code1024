package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

// resolveNativeModelModifier resolves @thinking/@effort/@temperature/@topp
// modifiers for native HTTP endpoints using the shared RelayKit modifier engine
// and hot-editable exemptions policy. When neither clientModel nor mappedModel
// contains active modifiers, it returns applied=false with the original body
// untouched.
func resolveNativeModelModifier(ctx context.Context, body []byte, clientModel, mappedModel, format string) (context.Context, []byte, modelmodifier.Spec, bool, error) {
	spec, err := modelmodifier.Resolve(clientModel, mappedModel)
	if err != nil {
		return ctx, body, modelmodifier.Spec{}, false, err
	}
	if !spec.HasControls() && modelmodifier.Base(clientModel) == clientModel && spec.Base == mappedModel {
		return ctx, body, spec, false, nil
	}
	nextBody := body
	if spec.HasControls() {
		nextBody, err = spec.ApplyForFormat(nextBody, format)
		if err != nil {
			return ctx, body, modelmodifier.Spec{}, false, err
		}
	}
	if format != "gemini" && len(nextBody) > 0 {
		nextBody, err = sjson.SetBytes(nextBody, "model", spec.Base)
		if err != nil {
			return ctx, body, modelmodifier.Spec{}, false, err
		}
	}
	return spec.Context(ctx), nextBody, spec, true, nil
}

// writeNativeModelModifierError emits a local HTTP 400 in the client's protocol
// error format before any upstream I/O occurs.
func writeNativeModelModifierError(c *gin.Context, format string) {
	if c == nil {
		return
	}
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalModelConfiguration)
	msg := modelmodifier.ErrInvalid.Error()
	switch format {
	case "claude":
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", msg)
	case "gemini":
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    http.StatusBadRequest,
				"message": msg,
				"status":  "INVALID_ARGUMENT",
			},
		})
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "invalid_request_error",
				"code":    "invalid_model_modifier",
				"message": msg,
			},
		})
	}
}

// reconcileNativeOpenAIModifierResult restores the public client model identity,
// ensures UpstreamModel never carries raw @ modifiers, and aligns BillingModel
// and ReasoningEffort with any Request Processing override on the final body.
func reconcileNativeOpenAIModifierResult(ctx context.Context, result *OpenAIForwardResult, spec modelmodifier.Spec, clientModel string, appliedBody []byte, format string) {
	if result == nil {
		return
	}
	if strings.TrimSpace(clientModel) != "" {
		result.Model = clientModel
	}
	if result.UpstreamModel == "" || result.UpstreamModel == clientModel {
		result.UpstreamModel = spec.Base
	}
	finalBody := appliedBody
	reconciled := spec
	if attempt, _ := ctx.Value(nativeRequestPluginKey{}).(*nativeRequestPluginAttempt); attempt != nil && attempt.finalBody != nil {
		finalBody = attempt.finalBody
		if updated, err := spec.ReconcileForFormat(appliedBody, finalBody, format, false); err == nil {
			reconciled = updated
		}
	}
	if reconciled.Thinking {
		if names := reconciled.BillingNames(); len(names) > 0 {
			result.BillingModel = names[0]
		}
		if value := reconciled.Effort(); value != "" {
			effort := value
			result.ReasoningEffort = &effort
		}
	} else if result.BillingModel == "" || result.BillingModel == clientModel {
		result.BillingModel = spec.Base
	}
	if format == "claude" && finalBody != nil {
		if effort, explicit := requestpatch.ClaudeReasoningEffort(finalBody); explicit && effort != "" {
			result.ReasoningEffort = &effort
		}
	}
}

// reconcileNativeForwardModifierResult aligns ForwardResult fields after a
// native Anthropic/Gemini/Antigravity forward with resolved @ modifiers.
// For Gemini, billable ReasoningEffort is read strictly from explicit final
// thinkingLevel and is never inferred from thinkingBudget.
func reconcileNativeForwardModifierResult(ctx context.Context, result *ForwardResult, spec modelmodifier.Spec, clientModel string, appliedBody []byte, format string) {
	if result == nil {
		return
	}
	if strings.TrimSpace(clientModel) != "" {
		result.Model = clientModel
	}
	if result.UpstreamModel == "" || result.UpstreamModel == clientModel {
		result.UpstreamModel = spec.Base
	}
	finalBody := appliedBody
	finalFormat := format
	if attempt, _ := ctx.Value(nativeRequestPluginKey{}).(*nativeRequestPluginAttempt); attempt != nil && attempt.finalBody != nil {
		finalBody = attempt.finalBody
		if attempt.wireFormat != "" {
			finalFormat = attempt.wireFormat
		}
	}
	switch finalFormat {
	case "gemini":
		result.ReasoningEffort = extractGeminiReasoningEffortFromBody(finalBody)
	case "claude":
		if effort, explicit := requestpatch.ClaudeReasoningEffort(finalBody); explicit && effort != "" {
			result.ReasoningEffort = &effort
		} else if spec.Thinking {
			if value := spec.Effort(); value != "" {
				effort := value
				result.ReasoningEffort = &effort
			}
		}
	}
}
