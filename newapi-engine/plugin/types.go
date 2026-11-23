// Package plugin owns the independent New API JS registry and installation
// lifecycle. It has no knowledge of native Sub plugin packages or wallets.
package plugin

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

const Baseline = "48f9df96ea7c68eae04a0f3687869f232f48929f"

var ErrVersionConflict = errors.New("相同版本已存在不同源码，请增加插件版本号")
var ErrRegistryUnavailable = errors.New("plugin registry unavailable")

type Compatibility struct {
	Runnable bool     `json:"runnable"`
	Kinds    []string `json:"kinds"`
	Warnings []string `json:"warnings"`
	Blockers []string `json:"blockers"`
}

type AssessHost func(jsplugin.Meta, runner.Descriptor) Compatibility

type Runtime interface {
	With(context.Context, string, func(*runner.Client) error) error
}

type Version struct {
	ID            int64             `json:"id"`
	Key           string            `json:"key"`
	Version       string            `json:"version"`
	SHA256        string            `json:"sha256"`
	Origin        string            `json:"origin"`
	Source        string            `json:"-"`
	Meta          jsplugin.Meta     `json:"meta"`
	Capabilities  runner.Descriptor `json:"capabilities"`
	Enabled       bool              `json:"enabled"`
	Active        bool              `json:"active"`
	CreatedAt     time.Time         `json:"created_at"`
	Compatibility Compatibility     `json:"compatibility"`
}

type Repository interface {
	List(context.Context) ([]Version, error)
	Versions(context.Context, string) ([]Version, error)
	Get(context.Context, int64) (*Version, error)
	Install(context.Context, *Version, *int64) (*Version, error)
	Activate(context.Context, int64, bool) error
	SelectVersion(context.Context, int64) error
}

type InstallOptions struct {
	AllowBuiltinOverride bool
	ExpectedSHA256       string
	ExpectedKey          string
	ExpectedVersion      string
	Origin               string
}
