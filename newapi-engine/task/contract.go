// SPDX-License-Identifier: AGPL-3.0-or-later
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f,
// relay/channel/task/jsplugin/adaptor.go. See ../NOTICE.md.
// Package task is the shared task codec between plugins and the gateway.
package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

const MaxSnapshotBytes = 1 << 20

type Request struct {
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers"`
	Body           json.RawMessage   `json:"body"`
	Credentialless bool              `json:"credentialless"`
	Action         string            `json:"action"`
	Model          string            `json:"model"`
	RewriteModel   string            `json:"rewriteModel"`
	ResponseType   string            `json:"responseType"`
	BodyType       string            `json:"bodyType"`
	Parts          []Part            `json:"parts"`
	ContentPath    []any             `json:"contentPath"`
	MimeType       string            `json:"mimeType"`
}
type Part struct {
	Name     string          `json:"name"`
	Value    json.RawMessage `json:"value"`
	FileRef  string          `json:"fileRef"`
	Filename string          `json:"filename"`
}
type Submitted struct {
	TaskID    string          `json:"taskId"`
	TaskData  json.RawMessage `json:"taskData"`
	State     json.RawMessage `json:"state"`
	Immediate *Result         `json:"immediate"`
}
type Result struct {
	Status           string          `json:"status"`
	Progress         string          `json:"progress"`
	Reason           string          `json:"reason"`
	URL              string          `json:"url"`
	RemoteURL        string          `json:"remoteUrl"`
	State            json.RawMessage `json:"state"`
	CompletionTokens float64         `json:"completionTokens"`
	TotalTokens      float64         `json:"totalTokens"`
}
type Artifact struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	MimeType string `json:"mimeType,omitempty"`
}

var artifactKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)

func ValidArtifactKey(key string) bool { return artifactKey.MatchString(key) }

func Decode[T any](data []byte) (T, error) {
	var result T
	if len(data) > MaxSnapshotBytes || !json.Valid(data) {
		return result, errors.New("plugin result is invalid or exceeds the snapshot limit")
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, errors.New("invalid plugin result shape")
	}
	return result, nil
}

func (r *Request) Validate(base string, hosts []string, defaultMethod string) error {
	if r.URL == "" {
		return errors.New("plugin request URL is missing")
	}
	u, err := url.Parse(r.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return errors.New("plugin request URL must be HTTP(S) without userinfo or fragment")
	}
	if !r.Credentialless {
		if err = jsplugin.ValidateRequestURL(r.URL, base, hosts); err != nil {
			return err
		}
	}
	if err = jsplugin.ValidateRequestHeaders(r.Headers); err != nil {
		return err
	}
	if r.Method == "" {
		r.Method = defaultMethod
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return errors.New("unsupported plugin request method")
	}
	if r.Credentialless && r.Method != http.MethodGet && r.Method != http.MethodHead {
		return errors.New("credentialless requests must use GET or HEAD")
	}
	if r.ResponseType == "" {
		r.ResponseType = "json"
	}
	if r.ResponseType != "json" && r.ResponseType != "sse" {
		return errors.New("unsupported plugin response type")
	}
	if len(r.Body) > MaxSnapshotBytes || len(r.Parts) > 64 {
		return errors.New("plugin request exceeds contract limits")
	}
	return nil
}

// Content-only fields are deliberately ignored by submit/query descriptors.
func (r *Request) ValidateContent() error {
	if err := jsplugin.ValidateJSONPath(r.ContentPath); err != nil {
		return err
	}
	if r.MimeType != "" {
		if len(r.MimeType) > 255 || strings.ContainsAny(r.MimeType, "\r\n\x00") {
			return errors.New("invalid artifact MIME type")
		}
		if kind, _, err := mime.ParseMediaType(r.MimeType); err != nil || !strings.Contains(kind, "/") {
			return errors.New("invalid artifact MIME type")
		}
	}
	return nil
}

func (s Submitted) Validate() error {
	if len(s.TaskID) > 512 || strings.TrimSpace(s.TaskID) == "" {
		if s.Immediate == nil {
			return errors.New("plugin returned an empty taskId")
		}
	}
	if len(s.State) > MaxSnapshotBytes || len(s.TaskData) > MaxSnapshotBytes {
		return errors.New("submitted task snapshot exceeds limit")
	}
	if s.Immediate != nil {
		return s.Immediate.Validate()
	}
	return nil
}
func (r Result) Validate() error {
	switch r.Status {
	case "NOT_START", "SUBMITTED", "QUEUED", "IN_PROGRESS", "SUCCESS", "FAILURE", "UNKNOWN":
	default:
		return fmt.Errorf("unrecognized plugin task status %q", r.Status)
	}
	if len(r.Reason) > 4096 || len(r.State) > MaxSnapshotBytes || len(r.URL) > 8192 || len(r.RemoteURL) > 8192 {
		return errors.New("task result exceeds limit")
	}
	if r.CompletionTokens < 0 || r.TotalTokens < 0 {
		return errors.New("negative task usage")
	}
	return nil
}
func (r Result) ProgressPercent() int {
	value, err := strconv.ParseFloat(strings.TrimSuffix(r.Progress, "%"), 64)
	if err != nil || value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return int(value)
}
func DecodeArtifacts(data []byte) ([]Artifact, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		return nil, errors.New("plugin artifacts must be an array")
	}
	items, err := Decode[[]Artifact](data)
	if err != nil {
		return nil, err
	}
	if len(items) > 64 {
		return nil, errors.New("too many task artifacts")
	}
	seen := map[string]bool{}
	for _, item := range items {
		if !ValidArtifactKey(item.Key) || seen[item.Key] {
			return nil, errors.New("invalid or duplicate task artifact key")
		}
		seen[item.Key] = true
		switch item.Type {
		case "video", "audio", "image", "file":
		default:
			return nil, errors.New("invalid task artifact type")
		}
		if len(item.MimeType) > 255 {
			return nil, errors.New("artifact MIME type exceeds limit")
		}
		if item.MimeType != "" {
			if _, _, err = mime.ParseMediaType(item.MimeType); err != nil {
				return nil, errors.New("invalid task artifact MIME type")
			}
		}
	}
	return items, nil
}
