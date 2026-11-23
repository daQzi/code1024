// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// Task result projection follows QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f, relay/requestplugin/task_report.go.
// See internal/newapi/NOTICE.md. No credentials, headers or plugin state enter
// this deliberately separate report projection.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/tidwall/gjson"
)

const maxTaskReportEncrypted = generation.MaxReportEncrypted
const maxTaskReportSnapshot = 128 << 10
const maxTaskReportRequest = 64 << 10

type taskReportSnapshot struct {
	Call      RequestPluginCall         `json:"call"`
	Instances []int64                   `json:"instances"`
	Body      json.RawMessage           `json:"body"`
	Media     []jsplugin.TransformMedia `json:"media,omitempty"`
	Records   []RequestPluginRecord     `json:"records"`
	Username  string                    `json:"username,omitempty"`
	ResultURL string                    `json:"result_url,omitempty"`
}

func (s *NewAPIPluginService) sealTaskReport(snapshot taskReportSnapshot) (string, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) > maxTaskReportSnapshot || s.encryptor == nil {
		return "", errors.New("task report snapshot unavailable")
	}
	sealed, err := s.encryptor.Encrypt(string(raw))
	if err != nil || len(sealed) > maxTaskReportEncrypted {
		return "", errors.New("task report encryption unavailable")
	}
	return sealed, nil
}

func (s *NewAPIPluginService) snapshotTaskReport(ctx context.Context, key *APIKey, model string, body json.RawMessage, form bool) string {
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	plan := pinned.plan
	if !plan.HasStage("report") {
		return ""
	}
	var instances []int64
	for _, item := range plan.items {
		if slices.Contains(item.Stages, "report") && requestPluginMatchesChannelType(item, 61) {
			instances = append(instances, item.ID)
		}
	}
	if len(instances) == 0 {
		return ""
	}
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, 8<<20); err != nil {
		s.dropTaskReport("ingress_capacity")
		return ""
	}
	if plan.call.UserID != key.UserID || plan.call.TokenID != key.ID || plan.call.GroupID != key.Group.ID || plan.call.Model != model || plan.call.Format != "task" {
		s.dropTaskReport("snapshot")
		return ""
	}
	snapshot := taskReportSnapshot{Call: plan.call, Instances: instances, Records: slices.Clone(plan.records)}
	if key.User != nil {
		snapshot.Username = key.User.Username
	}
	snapshot.Call.ModelCandidates = nil
	// Private upload IDs are only for admission and binding, not reports.
	if !form && !gjson.GetBytes(body, "file_references").Exists() {
		snapshot.Body, snapshot.Media = taskReportRequest(body)
	}
	sealed, err := s.sealTaskReport(snapshot)
	if err != nil {
		s.dropTaskReport("snapshot")
	}
	return sealed
}

func (s *NewAPIPluginService) openTaskReport(task *GenerationTask) (taskReportSnapshot, error) {
	var snapshot taskReportSnapshot
	if len(task.RequestReportEncrypted) == 0 || len(task.RequestReportEncrypted) > maxTaskReportEncrypted {
		return snapshot, errors.New("invalid report snapshot")
	}
	raw, err := s.decryptRequestPluginConfig(task.RequestReportEncrypted)
	if err != nil || len(raw) > maxTaskReportSnapshot || json.Unmarshal(raw, &snapshot) != nil || len(snapshot.Instances) == 0 || len(snapshot.Instances) > 64 || len(snapshot.Body) > maxTaskReportRequest || len(snapshot.Records) > 128 || snapshot.Call.UserID != task.UserID || snapshot.Call.TokenID != task.APIKeyID || snapshot.Call.GroupID != task.GroupID || snapshot.Call.Model != task.Model || snapshot.Call.Format != "task" {
		return taskReportSnapshot{}, errors.New("invalid report identity or snapshot")
	}
	return snapshot, nil
}

func (s *NewAPIPluginService) appendTaskReportRecords(task *GenerationTask, records []RequestPluginRecord) {
	if task.RequestReportEncrypted == "" {
		return
	}
	snapshot, err := s.openTaskReport(task)
	if err == nil && len(snapshot.Records)+len(records) > 128 {
		err = errors.New("task report record limit")
	}
	if err == nil {
		snapshot.Records = append(snapshot.Records, records...)
		task.RequestReportEncrypted, err = s.sealTaskReport(snapshot)
	}
	if err != nil {
		task.RequestReportEncrypted = ""
		s.dropTaskReport("snapshot")
	}
}

func (s *NewAPIPluginService) setTaskReportResultURL(task *GenerationTask, address string) {
	if task.RequestReportEncrypted == "" || !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
		return
	}
	if len(address) > 8192 {
		s.dropTaskReport("result_url")
		return
	}
	snapshot, err := s.openTaskReport(task)
	if err == nil {
		snapshot.ResultURL = address
		task.RequestReportEncrypted, err = s.sealTaskReport(snapshot)
	}
	if err != nil {
		task.RequestReportEncrypted = ""
		s.dropTaskReport("snapshot")
	}
}

// Inline data is projected to placeholders before the 64 KiB persisted-body
// check. Reading at most the existing Task JSON bound keeps this work finite.
func taskReportRequest(body json.RawMessage) (json.RawMessage, []jsplugin.TransformMedia) {
	return projectRequestPluginMedia(body, maxTaskReportRequest)
}

func projectRequestPluginMedia(body json.RawMessage, limit int) (json.RawMessage, []jsplugin.TransformMedia) {
	if len(body) > MaxRequestPluginBody || !json.Valid(body) {
		return nil, nil
	}
	media := []jsplugin.TransformMedia{}
	nodes := 0
	var walk func(gjson.Result, string, int, string) (any, bool)
	walk = func(value gjson.Result, path string, depth int, mime string) (any, bool) {
		nodes++
		if nodes > 16384 || depth > 64 {
			return nil, false
		}
		if value.IsObject() {
			fields := map[string]any{}
			for _, key := range []string{"mimeType", "mime_type", "media_type"} {
				if candidate := value.Get(key); candidate.Type == gjson.String {
					mime = candidate.Str
					break
				}
			}
			valid := true
			value.ForEach(func(key, child gjson.Result) bool {
				pointer := strings.ReplaceAll(strings.ReplaceAll(key.Str, "~", "~0"), "/", "~1")
				fields[key.Str], valid = walk(child, path+"/"+pointer, depth+1, mime)
				return valid
			})
			return fields, valid
		}
		if value.IsArray() {
			items := []any{}
			valid := true
			value.ForEach(func(_, child gjson.Result) bool {
				var item any
				item, valid = walk(child, path+"/"+strconv.Itoa(len(items)), depth+1, mime)
				items = append(items, item)
				return valid
			})
			return items, valid
		}
		if value.Type != gjson.String {
			return json.RawMessage(value.Raw), true
		}
		text := value.Str
		encoded := ""
		if prefix, data, found := strings.Cut(text, ","); found && strings.HasPrefix(prefix, "data:") && strings.HasSuffix(prefix, ";base64") {
			mime, encoded = strings.TrimSuffix(strings.TrimPrefix(prefix, "data:"), ";base64"), data
		} else if strings.HasSuffix(path, "/data") && strings.Contains(mime, "/") {
			encoded = text
		}
		if encoded != "" {
			if len(media) >= 64 {
				return nil, false
			}
			size := base64.RawStdEncoding.DecodedLen(len(strings.TrimRight(encoded, "=")))
			media = append(media, jsplugin.TransformMedia{Index: len(media), Path: path, MimeType: mime, Bytes: max(0, size)})
			return "[media:" + strconv.Itoa(len(media)-1) + "]", true
		}
		return text, true
	}
	value, valid := walk(gjson.ParseBytes(body), "", 0, "")
	if !valid {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > limit {
		return nil, nil
	}
	return encoded, media
}

type taskReportText struct {
	Path string `json:"path"`
	Text string `json:"text"`
}
type taskReportResult struct {
	URLs      []string         `json:"urls"`
	Texts     []taskReportText `json:"texts"`
	Truncated bool             `json:"truncated"`
}

func projectTaskReportResult(data json.RawMessage, primary ...string) taskReportResult {
	result := taskReportResult{URLs: []string{}, Texts: []taskReportText{}}
	if len(primary) > 0 && (strings.HasPrefix(primary[0], "https://") || strings.HasPrefix(primary[0], "http://")) {
		result.URLs = append(result.URLs, primary[0])
	}
	if len(data) > 8<<20 {
		result.Truncated = true
		return result
	}
	if !json.Valid(data) {
		return result
	}
	textBytes, nodes := 0, 0
	var walk func(gjson.Result, string, int) bool
	walk = func(value gjson.Result, path string, depth int) bool {
		nodes++
		if depth > 64 || nodes > 16384 {
			result.Truncated = true
			return false
		}
		if value.IsObject() || value.IsArray() {
			index := 0
			value.ForEach(func(key, child gjson.Result) bool {
				name := key.String()
				if value.IsArray() {
					name = strconv.Itoa(index)
				}
				index++
				if path != "" {
					name = path + "." + name
				}
				return walk(child, name, depth+1)
			})
			return !result.Truncated
		}
		if value.Type != gjson.String || value.Str == "" || strings.HasPrefix(value.Str, "data:") || taskReportBase64(value.Str) {
			return true
		}
		if strings.HasPrefix(value.Str, "https://") || strings.HasPrefix(value.Str, "http://") {
			if slices.Contains(result.URLs, value.Str) {
				return true
			}
			if len(result.URLs) == 32 {
				result.Truncated = true
				return false
			}
			result.URLs = append(result.URLs, value.Str)
			return true
		}
		if len(result.Texts) == 256 {
			result.Truncated = true
			return false
		}
		text := value.Str
		if room := (1 << 20) - textBytes; len(text) > room {
			text = strings.ToValidUTF8(text[:room], "")
			result.Truncated = true
		}
		textBytes += len(text)
		result.Texts = append(result.Texts, taskReportText{Path: path, Text: text})
		return !result.Truncated
	}
	walk(gjson.ParseBytes(data), "", 0)
	return result
}

func taskReportBase64(text string) bool {
	return len(text) >= 1024 && !strings.ContainsFunc(text, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=' || r == '-' || r == '_')
	})
}
