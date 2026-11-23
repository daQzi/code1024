package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
)

type credentialEmailFixture struct {
	sent   []NotificationEmailSendInput
	err    error
	onSend func(NotificationEmailSendInput)
}

func (s *credentialEmailFixture) Send(_ context.Context, input NotificationEmailSendInput) error {
	s.sent = append(s.sent, input)
	if s.onSend != nil {
		s.onSend(input)
	}
	return s.err
}
func (s *credentialEmailFixture) ResolveRecipientLocale(context.Context, int64, string) string {
	return "zh"
}

func credentialNotifierFixture(t *testing.T, config string) (*pluginCredentialNotifier, *credentialEmailFixture) {
	t.Helper()
	settings := newNotificationEmailMemorySettingRepo()
	if config != "" {
		require.NoError(t, settings.Set(context.Background(), SettingKeyOpsEmailNotificationConfig, config))
	}
	sender := &credentialEmailFixture{}
	return &pluginCredentialNotifier{settings: settings, sender: sender, limiter: newSlidingWindowLimiter(0, time.Hour)}, sender
}

func TestPluginCredentialNotificationsUseConfiguredOpsPolicy(t *testing.T) {
	for _, scenario := range []struct {
		name, config string
		want         int
	}{
		{"missing-config", "", 0},
		{"disabled", `{"alert":{"enabled":false,"recipients":["ops@example.invalid"]}}`, 0},
		{"critical-only", `{"alert":{"enabled":true,"min_severity":"critical","recipients":["ops@example.invalid"]}}`, 0},
		{"enabled", `{"alert":{"enabled":true,"recipients":[" ops@example.invalid ","OPS@example.invalid",""]}}`, 1},
		{"rate-limited", `{"alert":{"enabled":true,"rate_limit_per_hour":1,"recipients":["first@example.invalid","second@example.invalid"]}}`, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			n, sender := credentialNotifierFixture(t, scenario.config)
			require.NoError(t, n.notify(context.Background(), pluginCredentialNotice{AccountID: 17, AccountName: "Fixture", PluginKey: "fixture-plugin", Identity: strings.Repeat("a", 64), PreviousStatus: "active", Status: "active", Message: jsplugin.LocalizedText{"zh": "上游额度将于明日重置", "en": "Quota resets tomorrow"}}))
			require.Len(t, sender.sent, scenario.want)
			if scenario.want > 0 {
				input := sender.sent[0]
				require.Equal(t, NotificationEmailEventOpsAlert, input.Event)
				require.Equal(t, "plugin_credential", input.SourceType)
				require.Equal(t, strings.Repeat("a", 64), input.SourceID)
				require.Contains(t, input.Variables["alert_description"], "上游额度将于明日重置")
			}
		})
	}
}

func TestPluginCredentialNotificationsRecoveryAndHTML(t *testing.T) {
	for _, include := range []bool{false, true} {
		config := `{"alert":{"enabled":true,"min_severity":"warning","include_resolved_alerts":false,"recipients":["ops@example.invalid"]}}`
		if include {
			config = strings.Replace(config, "false", "true", 1)
		}
		n, sender := credentialNotifierFixture(t, config)
		notice := pluginCredentialNotice{AccountID: 17, AccountName: `<script>account</script>`, PluginKey: "fixture-plugin", Identity: strings.Repeat("b", 64), PreviousStatus: "active", Status: "active", NativeRecovered: true, Message: jsplugin.LocalizedText{"zh": `<script>message</script>`}}
		require.NoError(t, n.notify(context.Background(), notice))
		if !include {
			require.Empty(t, sender.sent)
			continue
		}
		require.Len(t, sender.sent, 1)
		preview, err := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil).PreviewTemplate(context.Background(), NotificationEmailPreviewInput{Event: NotificationEmailEventOpsAlert, Locale: "zh", Variables: sender.sent[0].Variables})
		require.NoError(t, err)
		require.NotContains(t, preview.HTML, "<script>")
		require.Contains(t, preview.HTML, "&lt;script&gt;")
	}
}

func TestPluginCredentialNotificationsNeverExposeSenderErrors(t *testing.T) {
	n, sender := credentialNotifierFixture(t, `{"alert":{"enabled":true,"recipients":["ops@example.invalid"]}}`)
	sender.err = errors.New("private-smtp-password-canary")
	err := n.notify(context.Background(), pluginCredentialNotice{Status: "auto_disabled", PreviousStatus: "active"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-smtp-password-canary")
}

func TestPluginCredentialNotificationHappensAfterCommitAndDoesNotBreakGrant(t *testing.T) {
	s, account, repo := lifecycleFixture(t, providerFixtureSource+lifecycleHooks)
	fixtureLogin(t, s, account)
	n, sender := credentialNotifierFixture(t, `{"alert":{"enabled":true,"recipients":["ops@example.invalid"]}}`)
	s.credentialNotifier = n
	sender.err = errors.New("private-delivery-canary")
	sender.onSend = func(input NotificationEmailSendInput) {
		state, err := repo.ReadPluginCredential(context.Background(), account.ID)
		require.NoError(t, err)
		require.Empty(t, state.LeaseToken, "credential commit has already released the lease")
		require.Contains(t, string(state.Public), "Maintenance completed")
		raw, err := json.Marshal(input)
		require.NoError(t, err)
		for _, private := range []string{"grant-1", "refresh-secret", state.Encrypted, account.GetCredential("api_key")} {
			require.NotContains(t, string(raw), private)
		}
		require.Regexp(t, "^[0-9a-f]{64}$", input.SourceID)
	}
	view, err := s.ManagePluginCredential(context.Background(), account.ID, 0, "", "", "scheduled", PluginLoginInput{})
	require.NoError(t, err)
	require.Equal(t, "active", view.Status)
	require.Empty(t, view.LastError)
	require.Len(t, sender.sent, 1)
	_, err = s.loadProviderLoginCredential(context.Background(), account, repo.versions[1], account.GetCredential("base_url"), "")
	require.NoError(t, err)
	// A path that does not actually execute scheduled must not resend stale
	// notice text left in the saved credential view.
	repo.versions[1].Capabilities.Hooks = slices.DeleteFunc(repo.versions[1].Capabilities.Hooks, func(hook string) bool { return hook == "scheduled" })
	_, err = s.ManagePluginCredential(context.Background(), account.ID, 0, "", "", "scheduled", PluginLoginInput{})
	require.NoError(t, err)
	require.Len(t, sender.sent, 1)
	// Failed state persistence cannot send a success notice.
	repo.versions[1].Capabilities.Hooks = append(repo.versions[1].Capabilities.Hooks, "scheduled")
	repo.fail = true
	_, err = s.ManagePluginCredential(context.Background(), account.ID, 0, "", "", "scheduled", PluginLoginInput{})
	require.Error(t, err)
	require.Len(t, sender.sent, 1)
}
