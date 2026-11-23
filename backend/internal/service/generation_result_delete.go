package service

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
)

var ErrGenerationTaskResultNotDeletable = errors.New("only a terminal, settled task result can be deleted")

type generationResultDeletionRepository interface {
	DeleteGenerationResult(ctx context.Context, id string, user, key, group int64, videoOnly bool) error
}

func (s *GenerationService) DeletePluginTaskResult(ctx context.Context, id string, key *APIKey, videoOnly bool) error {
	if !generation.IsPublicTaskID(id) || key == nil || key.GroupID == nil {
		return ErrGenerationTaskNotFound
	}
	repo, ok := s.repo.(generationResultDeletionRepository)
	if !ok {
		return errors.New("task result deletion is unavailable")
	}
	return repo.DeleteGenerationResult(ctx, id, key.UserID, key.ID, *key.GroupID, videoOnly)
}
