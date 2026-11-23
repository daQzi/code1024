package market

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/plugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

var ErrSubmissionMissing = errors.New("插件投稿不存在或不可访问")
var ErrSubmissionConflict = errors.New("投稿版本或状态已变化，请刷新；已发布版本不可修改")

type Registry struct {
	repo    RegistryRepository
	plugins *plugin.Manager
	assess  plugin.AssessHost
}

func NewRegistry(repo RegistryRepository, plugins *plugin.Manager, assess plugin.AssessHost) *Registry {
	if plugins == nil {
		plugins = plugin.NewManager(nil, nil, nil)
	}
	if assess == nil {
		assess = plugins.Assess
	}
	return &Registry{repo: repo, plugins: plugins, assess: assess}
}

type Submission struct {
	ID            int64                `json:"id"`
	AuthorID      int64                `json:"author_id"`
	Key           string               `json:"key"`
	Version       string               `json:"version"`
	SHA256        string               `json:"sha256"`
	Source        string               `json:"source,omitempty"`
	Readme        string               `json:"readme"`
	Meta          jsplugin.Meta        `json:"meta"`
	Capabilities  runner.Descriptor    `json:"capabilities"`
	Compatibility plugin.Compatibility `json:"compatibility"`
	Status        string               `json:"status"`
	Revision      int64                `json:"revision"`
	ReviewReason  string               `json:"review_reason"`
	CreatedAt     time.Time            `json:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
	PublishedAt   *time.Time           `json:"published_at"`
}

type RegistryFilter struct {
	Owner          int64
	Status         string
	Page, PageSize int
}

type RegistryRepository interface {
	ListPluginSubmissions(context.Context, RegistryFilter) ([]Submission, int64, error)
	GetPluginSubmission(context.Context, int64) (*Submission, error)
	SavePluginDraft(context.Context, *Submission) (*Submission, error)
	TransitionPluginSubmission(context.Context, int64, int64, int64, string, string, bool) error
}

func AuthorNamespace(userID int64) string {
	return "community-" + strconv.FormatInt(userID, 36) + "-"
}

func (s *Registry) registry() (RegistryRepository, error) {
	if s.repo == nil {
		return nil, errors.New("plugin registry unavailable")
	}
	return s.repo, nil
}

func (s *Registry) InspectSource(ctx context.Context, source string) (*Submission, error) {
	item, err := s.plugins.Inspect(ctx, source)
	if err != nil {
		return nil, err
	}
	return &Submission{Source: source, Meta: item.Meta, Capabilities: item.Capabilities, Key: item.Key, Version: item.Version, SHA256: item.SHA256, Compatibility: item.Compatibility}, nil
}

func (s *Registry) SaveSubmission(ctx context.Context, author, id, revision int64, source, readme string) (*Submission, error) {
	if author <= 0 || id < 0 || revision < 0 || len(readme) > 65536 {
		return nil, plugin.InputError("投稿参数无效")
	}
	// Ownership is checked before compiling a draft supplied for another ID.
	repo, err := s.registry()
	if err != nil {
		return nil, err
	}
	if id > 0 {
		existing, err := repo.GetPluginSubmission(ctx, id)
		if err != nil || existing == nil || existing.AuthorID != author {
			return nil, ErrSubmissionMissing
		}
		if existing.Revision != revision || existing.Status != "draft" && existing.Status != "rejected" {
			return nil, ErrSubmissionConflict
		}
	}
	item, err := s.InspectSource(ctx, source)
	if err != nil {
		return nil, err
	}
	prefix := AuthorNamespace(author)
	if !strings.HasPrefix(item.Key, prefix) || len(item.Key) <= len(prefix) {
		return nil, plugin.InputError(fmt.Sprintf("社区插件 key 必须以 %s 开头，且总长不超过 30 个字符", prefix))
	}
	item.ID, item.AuthorID, item.Revision, item.Readme = id, author, revision, readme
	saved, err := repo.SavePluginDraft(ctx, item)
	if err == nil {
		saved.Compatibility = s.assess(saved.Meta, saved.Capabilities)
	}
	return saved, err
}

func (s *Registry) ListSubmissions(ctx context.Context, filter RegistryFilter) ([]Submission, int64, error) {
	repo, err := s.registry()
	if err != nil {
		return nil, 0, err
	}
	if filter.Owner < 0 || filter.Page < 1 || filter.Page > 100000 || filter.PageSize < 1 || filter.PageSize > 100 {
		return nil, 0, errors.New("投稿查询参数无效")
	}
	items, total, err := repo.ListPluginSubmissions(ctx, filter)
	for i := range items {
		items[i].Compatibility = s.assess(items[i].Meta, items[i].Capabilities)
	}
	return items, total, err
}

func (s *Registry) Submission(ctx context.Context, id, actor int64, admin bool) (*Submission, error) {
	repo, err := s.registry()
	if err != nil {
		return nil, err
	}
	item, err := repo.GetPluginSubmission(ctx, id)
	if err != nil {
		return nil, err
	}
	if item == nil || actor <= 0 || !admin && item.AuthorID != actor && item.Status != "published" {
		return nil, ErrSubmissionMissing
	}
	item.Compatibility = s.assess(item.Meta, item.Capabilities)
	return item, nil
}

func (s *Registry) TransitionSubmission(ctx context.Context, id, actor, revision int64, action, reason string, admin bool) error {
	if actor <= 0 || revision < 1 || len(reason) > 2000 {
		return plugin.InputError("投稿状态参数无效")
	}
	if action != "submit" && action != "withdraw" && action != "publish" && action != "reject" {
		return plugin.InputError("投稿操作无效")
	}
	if (action == "publish" || action == "reject") && !admin {
		return ErrSubmissionMissing
	}
	if (action == "reject" || action == "withdraw") && len(strings.TrimSpace(reason)) < 4 {
		return plugin.InputError("请填写至少 4 个字符的原因")
	}
	repo, err := s.registry()
	if err != nil {
		return err
	}
	return repo.TransitionPluginSubmission(ctx, id, actor, revision, action, reason, admin)
}

func (s *Registry) InstallSubmission(ctx context.Context, id, actor int64, hash string) (*plugin.Version, error) {
	item, err := s.Submission(ctx, id, actor, true)
	if err != nil {
		return nil, err
	}
	if item.Status != "published" || item.SHA256 != hash {
		return nil, ErrSubmissionConflict
	}
	version, err := s.plugins.Install(ctx, item.Source, "", &actor, plugin.InstallOptions{ExpectedSHA256: hash, ExpectedKey: item.Key, ExpectedVersion: item.Version, Origin: "community:" + strconv.FormatInt(item.ID, 10)})
	return version, err
}
