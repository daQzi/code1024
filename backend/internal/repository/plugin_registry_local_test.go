package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGenerationLocalPostgresCommunityPluginLifecycle(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var author, admin int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash) VALUES('author@example.invalid','fixture') RETURNING id`).Scan(&author))
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,role) VALUES('reviewer@example.invalid','fixture','admin') RETURNING id`).Scan(&admin))
	repo := NewNewAPIPluginRepository(db)
	registry := repo.(service.PluginRegistryRepository)
	factories, err := service.PluginFactories()
	require.NoError(t, err)
	meta, capabilities := factories[0].Meta, factories[0].Capabilities
	meta.Key, meta.Version = service.PluginAuthorNamespace(author)+"fixture", "1.0.0"
	draft, err := registry.SavePluginDraft(ctx, &service.PluginSubmission{AuthorID: author, Key: meta.Key, Version: meta.Version, SHA256: strings.Repeat("a", 64), Source: "// inert fixture", Readme: "fixture readme", Meta: meta, Capabilities: capabilities})
	require.NoError(t, err)
	require.Equal(t, "draft", draft.Status)
	items, total, err := registry.ListPluginSubmissions(ctx, service.PluginRegistryFilter{Owner: admin, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, items)
	items, total, err = registry.ListPluginSubmissions(ctx, service.PluginRegistryFilter{Owner: author, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Empty(t, items[0].Source)
	require.ErrorIs(t, registry.TransitionPluginSubmission(ctx, draft.ID, admin, draft.Revision, "submit", "", false), service.ErrPluginSubmissionMissing)
	require.NoError(t, registry.TransitionPluginSubmission(ctx, draft.ID, author, draft.Revision, "submit", "", false))
	submitted, err := registry.GetPluginSubmission(ctx, draft.ID)
	require.NoError(t, err)
	require.ErrorIs(t, registry.TransitionPluginSubmission(ctx, draft.ID, author, submitted.Revision, "publish", "", false), service.ErrPluginSubmissionConflict)
	draft.Revision = submitted.Revision
	_, err = registry.SavePluginDraft(ctx, draft)
	require.ErrorIs(t, err, service.ErrPluginSubmissionConflict, "submitted source cannot change while being reviewed")
	require.NoError(t, registry.TransitionPluginSubmission(ctx, draft.ID, admin, submitted.Revision, "publish", "reviewed fixture", true))
	published, err := registry.GetPluginSubmission(ctx, draft.ID)
	require.NoError(t, err)
	require.NotNil(t, published.PublishedAt)
	v := &service.NewAPIPluginVersion{Key: published.Key, Version: published.Version, SHA256: published.SHA256, Origin: fmt.Sprintf("community:%d", published.ID), Source: published.Source, Meta: published.Meta, Capabilities: published.Capabilities}
	installed, err := repo.Install(ctx, v, &admin)
	require.NoError(t, err)
	require.False(t, installed.Enabled, "publication and installation must not enable execution")
	require.NoError(t, registry.TransitionPluginSubmission(ctx, published.ID, author, published.Revision, "withdraw", "withdraw fixture", false))
	_, err = repo.Install(ctx, v, &admin)
	require.ErrorIs(t, err, service.ErrPluginSubmissionConflict, "withdrawal blocks new installs, even an idempotent install")
	_, err = repo.Get(ctx, installed.ID)
	require.NoError(t, err, "withdrawal does not delete installed/in-flight versions")
	var events int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sub2api_plugin_submission_events WHERE submission_id=$1`, draft.ID).Scan(&events))
	require.Equal(t, 4, events)
}
