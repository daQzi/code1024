package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

func TestPluginVersionDeleteLocalPostgres(t *testing.T) {
	db := generationLocalDB(t)
	repo := NewNewAPIPluginRepository(db).(*newAPIPluginRepository)
	ctx := context.Background()
	install := func(key, version, digest, origin string) *service.NewAPIPluginVersion {
		t.Helper()
		item, err := repo.Install(ctx, &service.NewAPIPluginVersion{
			Key: key, Version: version, SHA256: strings.Repeat(digest, 64), Origin: origin, Source: "fixture",
			Meta: jsplugin.Meta{Name: "Fixture", Key: key, Version: version, Models: []string{"video"}}, Capabilities: runner.Descriptor{Task: true},
		}, nil)
		require.NoError(t, err)
		return item
	}

	factory := install("fixture", "1.0.0", "a", "builtin:fixture")
	custom := install("fixture", "2.0.0", "b", "uploaded")
	require.NoError(t, repo.Activate(ctx, custom.ID, true))
	result, err := repo.DeleteVersion(ctx, custom.ID, func(service.NewAPIPluginVersion) bool { return false })
	require.NoError(t, err)
	require.Equal(t, factory.ID, *result.FallbackVersionID)
	restored, err := repo.Get(ctx, factory.ID)
	require.NoError(t, err)
	require.True(t, restored.Active)
	require.False(t, restored.Enabled)
	_, err = repo.Get(ctx, custom.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = repo.DeleteVersion(ctx, factory.ID, nil)
	require.ErrorIs(t, err, service.ErrPluginBuiltinProtected)

	bound := install("bound", "1.0.0", "c", "uploaded")
	var accountID int64
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('Bound','openai','apikey',jsonb_build_object('newapi_plugin_version_id',$1::bigint,'api_key','private-key','newapi_video_pricing','{"video":{"mode":"fixed","unit":"second","unit_price":"0.15"}}'::jsonb),1,1) RETURNING id`, bound.ID).Scan(&accountID))
	_, err = repo.DeleteVersion(ctx, bound.ID, nil)
	require.ErrorContains(t, err, "账号 1 个")
	pricing, err := repo.ListPluginAccountPricing(ctx, "bound")
	require.NoError(t, err)
	require.Len(t, pricing, 1)
	require.Equal(t, "0.15", pricing[0].PricingRules["video"].UnitPrice.String())
	_, err = db.Exec(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('Malformed','openai','apikey',jsonb_build_object('newapi_plugin_version_id',$1::bigint,'newapi_video_pricing','"not-a-map"'::jsonb),1,2)`, bound.ID)
	require.NoError(t, err)
	pricingWithBrokenRow, err := repo.ListPluginAccountPricing(ctx, "bound")
	require.NoError(t, err)
	require.Len(t, pricingWithBrokenRow, 2)
	require.Empty(t, pricingWithBrokenRow[1].PricingRules)
	encoded, err := json.Marshal(pricing)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-key")
	require.NotContains(t, string(encoded), "credentials")

	instanceVersion := install("instance", "1.0.0", "d", "uploaded")
	_, err = db.Exec(`INSERT INTO sub2api_request_plugin_instances(name,plugin_version_id,scope,config_encrypted) VALUES('Fixture',$1,'global','{}')`, instanceVersion.ID)
	require.NoError(t, err)
	_, err = repo.DeleteVersion(ctx, instanceVersion.ID, nil)
	require.ErrorContains(t, err, "请求处理实例 1 个")

	standalone := install("standalone", "1.0.0", "e", "uploaded")
	result, err = repo.DeleteVersion(ctx, standalone.ID, nil)
	require.NoError(t, err)
	require.True(t, result.RemovedPlugin)
}

func TestPluginVersionDeleteLocalPostgresConcurrentAccountBinding(t *testing.T) {
	db := generationLocalDB(t)
	repo := NewNewAPIPluginRepository(db).(*newAPIPluginRepository)
	ctx := context.Background()
	version, err := repo.Install(ctx, &service.NewAPIPluginVersion{Key: "racing", Version: "1.0.0", SHA256: strings.Repeat("f", 64), Source: "fixture", Meta: jsplugin.Meta{Name: "Fixture"}}, nil)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('Binding','openai','apikey',jsonb_build_object('newapi_plugin_version_id',$1::bigint),1,1)`, version.ID)
	require.NoError(t, err)
	deletion := make(chan error, 1)
	go func() { _, deleteErr := repo.DeleteVersion(ctx, version.ID, nil); deletion <- deleteErr }()
	select {
	case deleteErr := <-deletion:
		t.Fatalf("deletion must wait for the binding transaction: %v", deleteErr)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, tx.Commit())
	select {
	case deleteErr := <-deletion:
		require.ErrorContains(t, deleteErr, "账号 1 个")
	case <-time.After(5 * time.Second):
		t.Fatal("deletion did not complete after binding committed")
	}
	_, err = db.Exec(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('Missing','openai','apikey','{"newapi_plugin_version_id":999999}',1,1)`)
	require.Error(t, err)
}
