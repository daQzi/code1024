package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

func TestGenerationLocalPostgresRequestPluginSnapshotsAndRevision(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user, group int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash) VALUES('request-plugin@example.invalid','test') RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('request-plugin','openai') RETURNING id`).Scan(&group))
	plugins := NewNewAPIPluginRepository(db)
	repo := plugins.(service.RequestPluginRepository)
	source, err := builtin.Source("request-filter")
	require.NoError(t, err)
	loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	v, err := plugins.Install(ctx, &service.NewAPIPluginVersion{Key: loaded.Meta.Key, Version: loaded.Meta.Version, Source: source, SHA256: strings.Repeat("a", 64), Meta: loaded.Meta, Capabilities: runner.Describe(loaded)}, &user)
	require.NoError(t, err)
	item := service.RequestPluginInstance{Name: "Scoped policy", PluginVersionID: v.ID, Enabled: true, Scope: "group", GroupIDs: []int64{group}, Models: []string{"gpt-fixture"}, Stages: []string{"ingress"}, ConfigEncrypted: `ENC:{"rules":[]}`}
	saved, err := repo.SaveRequestPluginInstance(ctx, &item, user)
	require.NoError(t, err)
	require.EqualValues(t, 1, saved.Revision)
	require.Empty(t, saved.ConfigEncrypted)
	require.Nil(t, saved.Version)
	listed, err := repo.ListRequestPluginInstances(ctx, false)
	require.NoError(t, err)
	raw, _ := json.Marshal(listed)
	require.NotContains(t, string(raw), "ENC:")
	require.NotContains(t, string(raw), "export const")
	execution, err := repo.ListRequestPluginInstances(ctx, true)
	require.NoError(t, err)
	require.Len(t, execution, 1)
	require.Equal(t, item.ConfigEncrypted, execution[0].ConfigEncrypted)
	require.Equal(t, source, execution[0].Version.Source)
	require.False(t, execution[0].Version.Enabled, "instance activation does not depend on the global task/provider flag")
	full, err := repo.GetRequestPluginInstance(ctx, saved.ID)
	require.NoError(t, err)
	full.Enabled = false
	updated, err := repo.SaveRequestPluginInstance(ctx, full, user)
	require.NoError(t, err)
	require.EqualValues(t, 2, updated.Revision)
	// Legacy empty format scopes remain all formats; Task egress is explicit.
	full, err = repo.GetRequestPluginInstance(ctx, saved.ID)
	require.NoError(t, err)
	full.Stages = []string{"egress"}
	_, err = repo.SaveRequestPluginInstance(ctx, full, user)
	require.Error(t, err, "database also rejects accidental unscoped egress")
	full.Formats = []string{"task"}
	updated, err = repo.SaveRequestPluginInstance(ctx, full, user)
	require.NoError(t, err)
	require.Equal(t, []string{"task"}, updated.Formats)
	require.Equal(t, []string{"egress"}, updated.Stages)
	full, err = repo.GetRequestPluginInstance(ctx, saved.ID)
	require.NoError(t, err)
	full.Stages = []string{"report"}
	updated, err = repo.SaveRequestPluginInstance(ctx, full, user)
	require.NoError(t, err)
	require.Equal(t, []string{"report"}, updated.Stages)
	_, err = repo.SaveRequestPluginInstance(ctx, full, user)
	require.ErrorIs(t, err, service.ErrRequestPluginConflict)
	execution, err = repo.ListRequestPluginInstances(ctx, true)
	require.NoError(t, err)
	require.Empty(t, execution)
	item.GroupIDs = []int64{999999}
	_, err = repo.SaveRequestPluginInstance(ctx, &item, user)
	require.Error(t, err)
	run := service.RequestPluginRun{RequestID: "fixture-request", UserID: user, GroupID: group, Model: "gpt-fixture", Format: "openai", Records: []service.RequestPluginRecord{{InstanceID: saved.ID, VersionID: v.ID, Stage: "ingress", Action: "reject", DurationMS: 2}}}
	require.NoError(t, repo.RecordRequestPluginRun(ctx, run))
	runs, total, err := repo.ListRequestPluginRuns(ctx, run.RequestID, 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, run.Records, runs[0].Records)
	_, total, err = repo.ListRequestPluginRuns(ctx, "other-request", 1, 20)
	require.NoError(t, err)
	require.Zero(t, total)
	var account int64
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('provider-policy','openai','apikey','{}',1,1) RETURNING id`).Scan(&account))
	provider := service.RequestPluginInstance{Name: "Provider policy", PluginVersionID: v.ID, Scope: "account", GroupIDs: []int64{}, AccountIDs: []int64{account}, Models: []string{}, Formats: []string{"openai", "openai_responses"}, Stages: []string{"ingress", "request", "outbound", "egress", "reply", "report"}, ConfigEncrypted: `ENC:{}`, Enabled: true}
	savedProvider, err := repo.SaveRequestPluginInstance(ctx, &provider, user)
	require.NoError(t, err)
	require.Equal(t, provider.AccountIDs, savedProvider.AccountIDs)
	require.False(t, savedProvider.NetworkEnabled, "network is never granted by creating or upgrading an instance")
	require.False(t, savedProvider.ReplyNetworkEnabled, "ingress grants never enable sending model replies")
	fullProvider, err := repo.GetRequestPluginInstance(ctx, savedProvider.ID)
	require.NoError(t, err)
	require.Equal(t, provider.Stages, fullProvider.Stages)
	for _, change := range []func(*service.RequestPluginInstance){
		func(i *service.RequestPluginInstance) { i.AccountIDs = []int64{} },
		func(i *service.RequestPluginInstance) { i.AccountIDs = []int64{99999999} },
		func(i *service.RequestPluginInstance) { i.GroupIDs = []int64{group} },
		func(i *service.RequestPluginInstance) { i.Formats = []string{"task"} },
		func(i *service.RequestPluginInstance) {
			i.Stages = []string{"ingress", "request", "outbound", "egress", "reply", "report", "report"}
		},
		func(i *service.RequestPluginInstance) { i.Stages = []string{"unknown"} },
	} {
		invalid := provider
		change(&invalid)
		_, err = repo.SaveRequestPluginInstance(ctx, &invalid, user)
		require.Error(t, err, "unsupported account stage/scope must also fail at persistence")
	}
	fullProvider.Enabled = false
	fullProvider.NetworkEnabled = true
	withGrant, err := repo.SaveRequestPluginInstance(ctx, fullProvider, user)
	require.NoError(t, err)
	require.True(t, withGrant.NetworkEnabled)
	restored, err := repo.GetRequestPluginInstance(ctx, fullProvider.ID)
	require.NoError(t, err)
	require.True(t, restored.NetworkEnabled, "network permission survives restart via the repository")
	invalidGrant := *restored
	invalidGrant.Stages = []string{"report"}
	_, err = repo.SaveRequestPluginInstance(ctx, &invalidGrant, user)
	require.Error(t, err, "stage networking cannot silently grant report, reply or request access")
	restored.NetworkEnabled = false
	revoked, err := repo.SaveRequestPluginInstance(ctx, restored, user)
	require.NoError(t, err)
	require.False(t, revoked.NetworkEnabled)
	replyReview, err := repo.GetRequestPluginInstance(ctx, fullProvider.ID)
	require.NoError(t, err)
	replyReview.ReplyNetworkEnabled = true
	replyGrant, err := repo.SaveRequestPluginInstance(ctx, replyReview, user)
	require.NoError(t, err)
	require.True(t, replyGrant.ReplyNetworkEnabled)
	restoredReply, err := repo.GetRequestPluginInstance(ctx, fullProvider.ID)
	require.NoError(t, err)
	require.True(t, restoredReply.ReplyNetworkEnabled, "reply grant survives repository reconstruction")
	invalidReply := *restoredReply
	invalidReply.Stages = []string{"ingress"}
	_, err = repo.SaveRequestPluginInstance(ctx, &invalidReply, user)
	require.Error(t, err, "reply access must be revoked when removing reply")
	restoredReply.ReplyNetworkEnabled = false
	revokedReply, err := repo.SaveRequestPluginInstance(ctx, restoredReply, user)
	require.NoError(t, err)
	require.False(t, revokedReply.ReplyNetworkEnabled)
	execution, err = repo.ListRequestPluginInstances(ctx, true)
	require.NoError(t, err)
	require.Empty(t, execution)
	for _, scope := range []string{"global", "group"} {
		report := service.RequestPluginInstance{Name: "HTTP scope report", PluginVersionID: v.ID, Scope: scope, GroupIDs: []int64{}, Models: []string{}, Stages: []string{"report"}, Formats: []string{"openai", "openai_responses"}, ConfigEncrypted: `ENC:{}`}
		if scope == "group" {
			report.GroupIDs = []int64{group}
		}
		httpReport, err := repo.SaveRequestPluginInstance(ctx, &report, user)
		require.NoError(t, err)
		restored, err := repo.GetRequestPluginInstance(ctx, httpReport.ID)
		require.NoError(t, err)
		require.Equal(t, report.Formats, restored.Formats)
		require.Equal(t, report.Stages, restored.Stages)
		for _, invalidFormats := range [][]string{nil, {"claude"}, {"gemini"}, {"task", "openai"}} {
			invalid := *restored
			invalid.Formats = invalidFormats
			_, err = repo.SaveRequestPluginInstance(ctx, &invalid, user)
			require.Error(t, err, "global/group reports must retain explicit supported formats")
		}
		restored.Stages = []string{"egress", "report"}
		restored, err = repo.SaveRequestPluginInstance(ctx, restored, user)
		require.NoError(t, err, "explicit HTTP egress is supported in global/group scope")
		restored, err = repo.GetRequestPluginInstance(ctx, restored.ID)
		require.NoError(t, err)
		restored.Stages = []string{"ingress", "report"}
		restored, err = repo.SaveRequestPluginInstance(ctx, restored, user)
		require.NoError(t, err)
		restored, err = repo.GetRequestPluginInstance(ctx, restored.ID)
		require.NoError(t, err)
		restored.Stages = []string{"ingress", "reply", "report"}
		restored.ReplyNetworkEnabled = true
		updated, err := repo.SaveRequestPluginInstance(ctx, restored, user)
		require.NoError(t, err)
		roundtrip, err := repo.GetRequestPluginInstance(ctx, updated.ID)
		require.NoError(t, err)
		require.Equal(t, []string{"ingress", "reply", "report"}, roundtrip.Stages)
		require.True(t, roundtrip.ReplyNetworkEnabled)
		for _, formats := range [][]string{nil, {"task"}, {"openai", "task"}, {"claude"}} {
			invalid := *roundtrip
			invalid.Formats = formats
			_, err := repo.SaveRequestPluginInstance(ctx, &invalid, user)
			require.Error(t, err, "HTTP reply retains explicit format isolation")
		}
		roundtrip.Stages = []string{"egress", "reply"}
		roundtrip, err = repo.SaveRequestPluginInstance(ctx, roundtrip, user)
		require.NoError(t, err)
		roundtrip, err = repo.GetRequestPluginInstance(ctx, roundtrip.ID)
		require.NoError(t, err)
		roundtrip.ReplyNetworkEnabled = false
		roundtrip.Stages = []string{"ingress", "request", "outbound", "egress", "reply", "report"}
		roundtrip, err = repo.SaveRequestPluginInstance(ctx, roundtrip, user)
		require.NoError(t, err)
		roundtrip, err = repo.GetRequestPluginInstance(ctx, roundtrip.ID)
		require.NoError(t, err)
		require.Len(t, roundtrip.Stages, 6)
		for _, formats := range [][]string{nil, {"task"}, {"claude"}, {"openai", "task"}} {
			invalid := *roundtrip
			invalid.Formats = formats
			_, err := repo.SaveRequestPluginInstance(ctx, &invalid, user)
			require.Error(t, err, "ordinary HTTP stages cannot be enabled in Task or unimplemented formats")
		}
	}
	require.NoError(t, ApplyMigrations(ctx, db))
}

func TestGenerationLocalPostgresRequestPluginChannelScopes(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash) VALUES('typed-policy@example.invalid','test') RETURNING id`).Scan(&user))
	source := `export const meta={apiVersion:1,key:"typed-policy",name:"Typed",version:"1.0.0",author:{name:"Fixture"},scope:{models:["public-video"],channelTypes:[1,61]}};export function ingress(){return {action:"continue"}}`
	loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	plugins := NewNewAPIPluginRepository(db)
	v, err := plugins.Install(ctx, &service.NewAPIPluginVersion{Key: loaded.Meta.Key, Version: loaded.Meta.Version, Source: source, SHA256: strings.Repeat("b", 64), Meta: loaded.Meta, Capabilities: runner.Describe(loaded)}, &user)
	require.NoError(t, err)
	repo := plugins.(service.RequestPluginRepository)
	saved, err := repo.SaveRequestPluginInstance(ctx, &service.RequestPluginInstance{Name: "Typed Task ingress", PluginVersionID: v.ID, Enabled: true, Scope: "global", GroupIDs: []int64{}, AccountIDs: []int64{}, Models: []string{}, Formats: []string{"task"}, Stages: []string{"ingress"}, ConfigEncrypted: `ENC:{}`}, user)
	require.NoError(t, err)
	restarted := NewNewAPIPluginRepository(db).(service.RequestPluginRepository)
	items, err := restarted.ListRequestPluginInstances(ctx, true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, saved.ID, items[0].ID)
	require.Equal(t, source, items[0].Version.Source)
	require.Equal(t, []int{1, 61}, items[0].Version.Meta.Scope.ChannelTypes)
	require.Equal(t, []string{"public-video"}, items[0].Version.Meta.Scope.Models)
	require.NoError(t, ApplyMigrations(ctx, db))
	items, err = restarted.ListRequestPluginInstances(ctx, true)
	require.NoError(t, err)
	require.Equal(t, []int{1, 61}, items[0].Version.Meta.Scope.ChannelTypes)
	public, err := restarted.ListRequestPluginInstances(ctx, false)
	require.NoError(t, err)
	require.Nil(t, public[0].Version)
	require.Empty(t, public[0].ConfigEncrypted)
}
