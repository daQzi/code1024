package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/tidwall/gjson"
)

var ErrGenerationRemixUnsupported = errors.New("the origin task does not support this remix operation")

type generationRemixKey struct{}

type generationRemix struct {
	origin, model, seconds, size string
}

// GenerationVideoRemixOrigin recognizes the two gateway-owned aliases. It does
// not accept an upstream ID or a nested path as a public video identity.
func GenerationVideoRemixOrigin(path string) (string, bool) {
	path = strings.TrimPrefix(path, "/v1")
	path, ok := strings.CutPrefix(path, "/videos/")
	if !ok {
		return "", false
	}
	id, ok := strings.CutSuffix(path, "/remix")
	return id, ok && id != "" && !strings.Contains(id, "/")
}

// WithGenerationVideoRemix fixes the route's parent before replay. Including it
// in the request fingerprint prevents an accepted key from naming another
// parent or an ordinary video create, even when their JSON is identical.
func WithGenerationVideoRemix(ctx context.Context, origin string) context.Context {
	return context.WithValue(ctx, generationRemixKey{}, generationRemix{origin: origin})
}

func generationVideoRemixVersion(version *NewAPIPluginVersion) bool {
	if version == nil || !version.Capabilities.Task || version.Meta.Key != "sora" {
		return false
	}
	// The baseline has no generic remix capability declaration. Only the exact
	// shipped Sora driver is known to honor ctx.action=remix; an unrelated or
	// modified script must not silently submit a new video instead.
	source, err := builtin.Source("sora")
	return err == nil && version.Source == source
}

// PrepareVideoRemix resolves a parent under the caller's full ownership and
// supplies its public model before allowlists, request plugins and scheduling.
// The existing origin admission then pins and transactionally rechecks the
// source connection and login. No upstream request or reservation occurs here.
func (s *GenerationService) PrepareVideoRemix(ctx context.Context, key *APIKey, body json.RawMessage) (context.Context, json.RawMessage, error) {
	remix, ok := ctx.Value(generationRemixKey{}).(generationRemix)
	if !ok || len(remix.origin) != 46 || !strings.HasPrefix(remix.origin, "video_") || key == nil || key.Group == nil || s.plugins == nil {
		return nil, nil, ErrGenerationOrigin
	}
	if _, err := hex.DecodeString(remix.origin[len("video_"):]); err != nil {
		return nil, nil, ErrGenerationOrigin
	}
	if len(body) > 1<<20 || !utf8.Valid(body) || !json.Valid(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, nil, ErrGenerationDecode
	}
	prompt, valid := "", true
	count := 0
	gjson.ParseBytes(body).ForEach(func(name, value gjson.Result) bool {
		count++
		if name.Str != "prompt" || value.Type != gjson.String || count != 1 {
			valid = false
			return false
		}
		prompt = value.Str
		return true
	})
	if !valid || strings.TrimSpace(prompt) == "" || len(prompt) > 8192 {
		return nil, nil, ErrGenerationDecode
	}
	parent, err := s.repo.GetOwned(ctx, remix.origin, key.UserID, key.ID)
	if err != nil {
		return nil, nil, err
	}
	if parent == nil || parent.GroupID != key.Group.ID || parent.Status != GenerationSucceeded || parent.UpstreamID == "" || parent.Model == "" {
		return nil, nil, ErrGenerationOrigin
	}
	version, err := s.plugins.repo.Get(ctx, parent.PluginVersionID)
	if err != nil {
		return nil, nil, err
	}
	if !generationVideoRemixVersion(version) || !version.Enabled {
		return nil, nil, ErrGenerationRemixUnsupported
	}
	remix.model, remix.seconds, remix.size = parent.Model, parent.PublicFields.Seconds, parent.PublicFields.Size
	// These are inherited billing selectors, never new client multipliers. An
	// unknown old duration stays unknown rather than becoming a guessed 4 s.
	if remix.seconds != "" {
		seconds, err := strconv.ParseFloat(remix.seconds, 64)
		if err != nil || seconds <= 0 || seconds > 3600 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return nil, nil, ErrGenerationOrigin
		}
	}
	if len(remix.size) > 32 {
		return nil, nil, ErrGenerationOrigin
	}
	ctx = context.WithValue(ctx, generationRemixKey{}, remix)
	ctx, err = WithGenerationClientRequest(ctx, body)
	if err != nil {
		return nil, nil, err
	}
	prepared, err := json.Marshal(map[string]string{"model": parent.Model, "prompt": prompt})
	return ctx, prepared, err
}

func generationRemixEstimatedFacts(remix generationRemix) json.RawMessage {
	facts := make(map[string]any)
	if remix.seconds != "" {
		// PrepareVideoRemix already checked its finite positive bound. Preserve
		// the exact decimal spelling instead of passing through float rounding.
		facts["seconds"] = json.Number(remix.seconds)
	}
	if remix.size != "" {
		facts["size"] = remix.size
	}
	raw, _ := json.Marshal(facts)
	return raw
}
