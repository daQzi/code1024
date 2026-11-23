package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

// ValidationError carries no HTTP or framework policy. The embedding host
// translates it to its existing public input-error response.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func InputError(message string) error    { return &ValidationError{Message: message} }

func (m *Manager) Assess(meta jsplugin.Meta, descriptor runner.Descriptor) Compatibility {
	return m.assess(meta, descriptor)
}

// Inspect executes untrusted metadata only through the supplied isolated
// runtime. Inspection neither installs nor enables a script.
func (m *Manager) Inspect(ctx context.Context, source string) (*Version, error) {
	if len(source) == 0 || len(source) > runner.MaxSourceBytes || !utf8.ValidString(source) {
		return nil, InputError("插件源码不能为空或超过 1 MiB")
	}
	if m.runtime == nil {
		return nil, errors.New("plugin runtime unavailable")
	}
	item := &Version{Source: source}
	err := m.runtime.With(ctx, source, func(client *runner.Client) error {
		item.Meta, item.Capabilities = client.Meta, client.Descriptor
		return nil
	})
	if err != nil {
		return nil, InputError(err.Error())
	}
	item.Key, item.Version = item.Meta.Key, item.Meta.Version
	hash := sha256.Sum256([]byte(source))
	item.SHA256 = hex.EncodeToString(hash[:])
	item.Compatibility = m.assess(item.Meta, item.Capabilities)
	return item, nil
}

type Source struct {
	ID      int64  `json:"id,omitempty"`
	Key     string `json:"key"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Source  string `json:"source"`
}

// CheckedSource reads an immutable artifact; it never evaluates the code.
func CheckedSource(source Source) (*Source, error) {
	if len(source.Source) == 0 || len(source.Source) > runner.MaxSourceBytes || !utf8.ValidString(source.Source) {
		return nil, errors.New("plugin source artifact is invalid")
	}
	hash := sha256.Sum256([]byte(source.Source))
	if source.SHA256 != hex.EncodeToString(hash[:]) {
		return nil, errors.New("plugin source artifact digest mismatch")
	}
	return &source, nil
}
