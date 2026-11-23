package service

import (
	"context"
	"errors"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/newapi-engine/market"
	engineplugin "github.com/Wei-Shaw/sub2api/newapi-engine/plugin"
)

var ErrPluginSubmissionMissing = infraerrors.New(http.StatusNotFound, "PLUGIN_SUBMISSION_NOT_FOUND", "插件投稿不存在或不可访问").WithCause(market.ErrSubmissionMissing)
var ErrPluginSubmissionConflict = infraerrors.New(http.StatusConflict, "PLUGIN_SUBMISSION_CONFLICT", "投稿版本或状态已变化，请刷新；已发布版本不可修改").WithCause(market.ErrSubmissionConflict)

func pluginInputError(message string) error {
	return infraerrors.New(http.StatusBadRequest, "PLUGIN_VALIDATION_FAILED", message)
}
func pluginControlError(err error) error {
	var input *engineplugin.ValidationError
	switch {
	case errors.Is(err, market.ErrSubmissionMissing):
		return ErrPluginSubmissionMissing
	case errors.Is(err, market.ErrSubmissionConflict):
		return ErrPluginSubmissionConflict
	case errors.As(err, &input):
		return pluginInputError(input.Message)
	default:
		return err
	}
}

type PluginSubmission = market.Submission
type PluginRegistryFilter = market.RegistryFilter
type PluginRegistryRepository = market.RegistryRepository

func PluginAuthorNamespace(userID int64) string { return market.AuthorNamespace(userID) }
func (s *NewAPIPluginService) registryService() *market.Registry {
	repo, _ := s.repo.(PluginRegistryRepository)
	return market.NewRegistry(repo, s.pluginManager(), AssessPluginHost)
}
func (s *NewAPIPluginService) InspectSource(ctx context.Context, source string) (*PluginSubmission, error) {
	value, err := s.registryService().InspectSource(ctx, source)
	return value, pluginControlError(err)
}
func (s *NewAPIPluginService) SaveSubmission(ctx context.Context, author, id, revision int64, source, readme string) (*PluginSubmission, error) {
	value, err := s.registryService().SaveSubmission(ctx, author, id, revision, source, readme)
	return value, pluginControlError(err)
}
func (s *NewAPIPluginService) ListSubmissions(ctx context.Context, filter PluginRegistryFilter) ([]PluginSubmission, int64, error) {
	values, total, err := s.registryService().ListSubmissions(ctx, filter)
	return values, total, pluginControlError(err)
}
func (s *NewAPIPluginService) Submission(ctx context.Context, id, actor int64, admin bool) (*PluginSubmission, error) {
	value, err := s.registryService().Submission(ctx, id, actor, admin)
	return value, pluginControlError(err)
}
func (s *NewAPIPluginService) TransitionSubmission(ctx context.Context, id, actor, revision int64, action, reason string, admin bool) error {
	return pluginControlError(s.registryService().TransitionSubmission(ctx, id, actor, revision, action, reason, admin))
}
func (s *NewAPIPluginService) InstallSubmission(ctx context.Context, id, actor int64, hash string) (*NewAPIPluginVersion, error) {
	value, err := s.registryService().InstallSubmission(ctx, id, actor, hash)
	return value, pluginControlError(err)
}
