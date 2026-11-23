package service

import (
	"mime"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const imageHTTPPerformanceKey = "_image_http_performance"

func imageHTTPPerformance(c *gin.Context, count int) relaymetrics.Outcome {
	if value, ok := c.Get(imageHTTPPerformanceKey); ok {
		if outcome, ok := value.(relaymetrics.Outcome); ok && outcome != relaymetrics.Unknown {
			return outcome
		}
	}
	return mediaCountPerformance(count)
}

// Image output and money are separate evidence: a completed image followed by
// an error is still billable, but the whole reply must not become a success.
type imageStreamPerformance struct {
	terminal relaymetrics.Outcome
}

func (p *imageStreamPerformance) observe(body []byte) {
	if p.terminal == relaymetrics.Failure || p.terminal == relaymetrics.Ignored {
		return
	}
	if string(body) == "[DONE]" {
		p.terminal = relaymetrics.Success
		return
	}
	if !gjson.ValidBytes(body) {
		p.terminal = relaymetrics.Failure
		return
	}
	if terminal := relaymetrics.Terminal(body); terminal != relaymetrics.Unknown {
		p.terminal = terminal
		return
	}
	if gjson.GetBytes(body, "error").Type != gjson.Null {
		p.terminal = relaymetrics.HTTPFailure(body, 0)
		return
	}
	switch gjson.GetBytes(body, "type").String() {
	case "image_generation.completed", "image_edit.completed":
		p.terminal = relaymetrics.Success
	}
}

func (p *imageStreamPerformance) outcome(count int) relaymetrics.Outcome {
	if p.terminal == relaymetrics.Ignored {
		return relaymetrics.Ignored
	}
	if p.terminal == relaymetrics.Success {
		return mediaCountPerformance(count)
	}
	return relaymetrics.Failure
}

// These observations never change responses, usage or billing. An HTTP 200 or
// an estimated billable image count alone is not proof of generated output.
func mediaCountPerformance(count int) relaymetrics.Outcome {
	if count > 0 {
		return relaymetrics.Success
	}
	return relaymetrics.Failure
}

func embeddingsPerformance(body []byte) relaymetrics.Outcome {
	if !gjson.ValidBytes(body) || gjson.GetBytes(body, "error").Type != gjson.Null {
		return relaymetrics.Failure
	}
	data := gjson.GetBytes(body, "data")
	valid, seen := data.IsArray(), false
	data.ForEach(func(_, item gjson.Result) bool {
		seen = true
		embedding := item.Get("embedding")
		nonempty := false
		if embedding.IsArray() {
			embedding.ForEach(func(_, _ gjson.Result) bool {
				nonempty = true
				return false
			})
		} else if embedding.Type == gjson.String {
			nonempty = embedding.String() != ""
		}
		valid = valid && nonempty
		return valid
	})
	if valid && seen {
		return relaymetrics.Success
	}
	return relaymetrics.Failure
}

func voiceHTTPPerformance(mode, contentType string, body []byte) relaymetrics.Outcome {
	if len(body) == 0 {
		return relaymetrics.Failure
	}
	kind, _, _ := mime.ParseMediaType(contentType)
	switch mode {
	case "tts":
		if strings.HasPrefix(kind, "audio/") || kind == "application/octet-stream" {
			return relaymetrics.Success
		}
	case "stt":
		if kind == "text/plain" {
			return relaymetrics.Success
		}
		if kind == "application/json" && gjson.ValidBytes(body) && gjson.GetBytes(body, "error").Type == gjson.Null && gjson.GetBytes(body, "text").Type == gjson.String {
			return relaymetrics.Success
		}
	}
	return relaymetrics.Failure
}
