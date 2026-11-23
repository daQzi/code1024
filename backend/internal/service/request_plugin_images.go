package service

import (
	"context"
	"reflect"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/newapi/mediahost"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func nativeRequestPluginUpstreamModel(account *Account, body []byte, path, mapped string) string {
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if !isNativeImagesPluginPath(path) {
		return resolveOpenAIForwardModel(account, model, mapped)
	}
	// Match the image gateway's channel → account mapping order, including
	// its established default when the optional client model is omitted.
	if mapped = strings.TrimSpace(mapped); mapped != "" {
		model = mapped
	}
	if model == "" {
		model = DefaultOpenAIImagesModel
	}
	return account.GetMappedModel(model)
}

func isNativeImagesPluginPath(path string) bool {
	return path == openAIImagesGenerationsEndpoint || path == openAIImagesEditsEndpoint
}

func nativeRequestPluginBodyGuard(path string) func([]byte, []byte) bool {
	if !isNativeImagesPluginPath(path) {
		return nil
	}
	return func(before, after []byte) bool {
		// Images have already passed capability selection, moderation and
		// pricing admission. Late stages may sanitize the prompt and set a
		// business user tag; they must not change count, quality, format,
		// frame/SVG options, input media or future vendor price controls.
		controls := func(body []byte) map[string]any {
			result := map[string]any{}
			gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
				if key.Str != "prompt" && key.Str != "user" {
					result[key.Str] = value.Value()
				}
				return true
			})
			return result
		}
		return reflect.DeepEqual(controls(before), controls(after))
	}
}

// nativeImagesMultipartRequestPluginOutbound runs late ingress/outbound/egress
// stages against the multipart text-field JSON view plus read-only binary file
// metadata, then rebuilds only changed text parts so binary parts stay intact.
func (s *OpenAIGatewayService) nativeImagesMultipartRequestPluginOutbound(ctx context.Context, account *Account, path, model string, body []byte, contentType string) (RequestPluginOutcome, error) {
	out := RequestPluginOutcome{Body: body}
	attempt, _ := ctx.Value(nativeRequestPluginKey{}).(*nativeRequestPluginAttempt)
	if attempt == nil || !attempt.requiresAttempt {
		return out, nil
	}
	if account.ID != attempt.accountID || s.newAPIPlugins == nil || path != openAIImagesEditsEndpoint {
		return out, &RequestPluginFailure{Class: "identity"}
	}
	form, err := ParseRequestPluginMultipart(contentType, body)
	if err != nil {
		return out, &RequestPluginFailure{Class: "unsupported_body"}
	}
	guard := func(before, after []byte) bool {
		prompt := gjson.GetBytes(after, "prompt")
		if prompt.Exists() && (prompt.Type != gjson.String || strings.TrimSpace(prompt.Str) == "") {
			return false
		}
		return nativeRequestPluginBodyGuard(path)(before, after) && form.CheckView(before, after, true) == nil
	}
	stageOut, err := s.newAPIPlugins.relayRequestPluginStages(attempt.ctx, account, path, model, form.View, contract.AdvancedCustomRoute{}, requestPluginStageRun{
		attempt: attempt.attempt, stages: []string{"ingress", "outbound", "egress"}, format: "openai",
		bodyGuard: guard, protected: nativeRequestPluginProtectedHeader, multipart: form,
	})
	if err != nil {
		return out, err
	}
	rebuilt, err := form.Rebuild(stageOut.Body)
	if err != nil {
		return out, &RequestPluginFailure{Class: "guard"}
	}
	stageOut.Body = rebuilt
	attempt.finalBody = rebuilt
	return stageOut, nil
}

// OAuth Images either sends a native Images body or a host-built Responses
// tool call. Run policies on that actual body, keeping the selected image model
// as the upstream identity even when the bridge uses a different text model.
func (s *OpenAIGatewayService) nativeImagesOAuthRequestPluginOutbound(ctx context.Context, account *Account, path, model string, body []byte, direct bool) (RequestPluginOutcome, error) {
	out := RequestPluginOutcome{Body: body}
	attempt, _ := ctx.Value(nativeRequestPluginKey{}).(*nativeRequestPluginAttempt)
	if attempt == nil || !attempt.requiresAttempt {
		return out, nil
	}
	if account.ID != attempt.accountID || s.newAPIPlugins == nil || !isNativeImagesPluginPath(path) {
		return out, &RequestPluginFailure{Class: "identity"}
	}
	format := "openai"
	guard := func(before, after []byte) bool {
		prompt := gjson.GetBytes(after, "prompt")
		return prompt.Type == gjson.String && strings.TrimSpace(prompt.Str) != "" && nativeRequestPluginBodyGuard(path)(before, after)
	}
	if !direct {
		format = "openai_responses"
		guard = nativeImagesResponsesBodyGuard
	}
	stageBody := body
	var detachedFiles []mediahost.MultipartFile
	oversizedProjected := false
	if len(body) > MaxRequestPluginBody {
		var projErr error
		stageBody, detachedFiles, projErr = projectOversizedImagesOAuthBody(body)
		if projErr != nil {
			return out, &RequestPluginFailure{Class: "unsupported_body"}
		}
		oversizedProjected = true
	}
	out, err := s.newAPIPlugins.relayRequestPluginStages(attempt.ctx, account, path, model, stageBody, contract.AdvancedCustomRoute{}, requestPluginStageRun{
		attempt: attempt.attempt, stages: []string{"ingress", "outbound", "egress"}, format: format, bodyGuard: guard, mediaFiles: detachedFiles,
		protected: func(name string) bool {
			return nativeProtocolRequestProtectedHeader(name) || strings.EqualFold(name, "openai-beta") || strings.EqualFold(name, "accept")
		},
	})
	if err == nil {
		if oversizedProjected {
			finalBody := body
			if direct {
				if p := gjson.GetBytes(out.Body, "prompt"); p.Type == gjson.String {
					finalBody, err = sjson.SetBytes(finalBody, "prompt", p.Str)
				}
			} else {
				if p := gjson.GetBytes(out.Body, "input.0.content.0.text"); p.Type == gjson.String {
					finalBody, err = sjson.SetBytes(finalBody, "input.0.content.0.text", p.Str)
				}
			}
			if err == nil {
				if u := gjson.GetBytes(out.Body, "user"); u.Exists() {
					finalBody, err = sjson.SetRawBytes(finalBody, "user", []byte(u.Raw))
				} else if gjson.GetBytes(finalBody, "user").Exists() {
					finalBody, err = sjson.DeleteBytes(finalBody, "user")
				}
			}
			if err != nil {
				return out, &RequestPluginFailure{Class: "guard"}
			}
			out.Body = finalBody
		}
		attempt.finalBody = out.Body
	}
	return out, err
}

func nativeImagesResponsesBodyGuard(before, after []byte) bool {
	// Only the user's prompt and business user tag may change. Protect the
	// image tool, its price controls, input media and the bridge's text model,
	// instructions, reasoning, storage and streaming settings as one unit.
	prompt := gjson.GetBytes(after, "input.0.content.0.text")
	if prompt.Type != gjson.String || strings.TrimSpace(prompt.Str) == "" {
		return false
	}
	controls := func(body []byte) any {
		body, _ = sjson.SetBytes(body, "input.0.content.0.text", "")
		body, _ = sjson.DeleteBytes(body, "user")
		return gjson.ParseBytes(body).Value()
	}
	return reflect.DeepEqual(controls(before), controls(after))
}
