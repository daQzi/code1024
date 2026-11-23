package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
)

var ErrGenerationArtifactNotFound = errors.New("task or artifact not found")

func admitTaskArtifactProjection(ctx context.Context, t *GenerationTask) error {
	return pkghttputil.ReserveRequestBodyMemory(ctx, int64(len(t.Data)+len(t.State))*6+(1<<20))
}

func (s *GenerationService) GetPluginTask(ctx context.Context, id string, key *APIKey) (*GenerationTask, error) {
	if !generation.IsPublicTaskID(id) || key == nil || key.GroupID == nil {
		return nil, ErrGenerationTaskNotFound
	}
	task, err := s.Get(ctx, id, key.UserID, key.ID)
	if err != nil {
		return nil, err
	}
	if task == nil || task.GroupID != *key.GroupID || task.PluginVersionID <= 0 {
		return nil, ErrGenerationTaskNotFound
	}
	return task, nil
}

func (s *GenerationService) ListTaskArtifacts(ctx context.Context, t *GenerationTask) ([]pluginTask.Artifact, error) {
	if t.Status != GenerationSucceeded {
		return []pluginTask.Artifact{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := admitTaskArtifactProjection(ctx, t); err != nil {
		return nil, err
	}
	return s.plugins.ListPinnedTaskArtifacts(ctx, t)
}

func (s *newAPITaskSession) listTaskArtifacts(ctx context.Context, t *GenerationTask) ([]pluginTask.Artifact, error) {
	if !s.has("listArtifacts") || t.Status != GenerationSucceeded {
		return []pluginTask.Artifact{}, nil
	}
	// Listing is a pure projection of saved results. Do not grant fetch or
	// acquire/refresh an upstream token just to list the user's artifacts.
	raw, err := s.client.Call(ctx, "listArtifacts", nil, generation.TaskArtifactInput(t))
	if err != nil {
		return nil, err
	}
	return pluginTask.DecodeArtifacts(raw)
}

func (s *GenerationService) TaskArtifactContent(ctx context.Context, t *GenerationTask, method, artifact string, headers http.Header) (*http.Response, context.CancelFunc, error) {
	if !pluginTask.ValidArtifactKey(artifact) || method != http.MethodGet && method != http.MethodHead {
		return nil, nil, ErrGenerationArtifactNotFound
	}
	return s.generationContent(ctx, t, method, artifact, headers, true)
}

func (s *NewAPIPluginService) requireTaskArtifact(ctx context.Context, account *Account, t *GenerationTask, key string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := admitTaskArtifactProjection(ctx, t); err != nil {
		return err
	}
	return s.withGenerationSession(ctx, account, t, func(session *newAPITaskSession) error {
		items, err := session.listTaskArtifacts(ctx, t)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.Key == key {
				return nil
			}
		}
		return ErrGenerationArtifactNotFound
	})
}
