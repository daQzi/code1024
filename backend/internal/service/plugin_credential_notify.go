package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

type pluginCredentialEmailSender interface {
	Send(context.Context, NotificationEmailSendInput) error
	ResolveRecipientLocale(context.Context, int64, string) string
}

type pluginCredentialNotifier struct {
	settings SettingRepository
	sender   pluginCredentialEmailSender
	limiter  *slidingWindowLimiter
}

type pluginCredentialNotice struct {
	AccountID              int64
	AccountName            string
	PluginKey              string
	Identity               string // Digest of the saved encrypted state, never the state itself.
	PreviousStatus, Status string
	NativeRecovered        bool
	Message                jsplugin.LocalizedText
}

func (n *pluginCredentialNotifier) notify(ctx context.Context, notice pluginCredentialNotice) error {
	if n == nil || n.settings == nil || n.sender == nil {
		return nil
	}
	changed := notice.PreviousStatus != notice.Status && (notice.PreviousStatus == "auto_disabled" || notice.Status == "auto_disabled")
	if len(notice.Message) == 0 && !changed && !notice.NativeRecovered {
		return nil
	}
	raw, err := n.settings.GetValue(ctx, SettingKeyOpsEmailNotificationConfig)
	if errors.Is(err, ErrSettingNotFound) {
		return nil
	}
	if err != nil {
		return errors.New("plugin notification settings unavailable")
	}
	var config OpsEmailNotificationConfig
	if json.Unmarshal([]byte(raw), &config) != nil {
		return errors.New("plugin notification settings invalid")
	}
	normalizeOpsEmailNotificationConfig(&config)
	if !config.Alert.Enabled || len(config.Alert.Recipients) == 0 {
		return nil
	}
	resolved := (notice.PreviousStatus == "auto_disabled" && notice.Status != "auto_disabled" || notice.NativeRecovered)
	if resolved && !config.Alert.IncludeResolvedAlerts {
		return nil
	}
	severity := "P1"
	if !shouldSendOpsAlertEmailByMinSeverity(config.Alert.MinSeverity, severity) {
		return nil
	}
	n.limiter.SetLimit(config.Alert.RateLimitPerHour)
	seen := make(map[string]bool)
	failed := false
	for _, recipient := range config.Alert.Recipients {
		recipient = strings.TrimSpace(recipient)
		key := strings.ToLower(recipient)
		if recipient == "" || seen[key] {
			continue
		}
		seen[key] = true
		if ctx.Err() != nil {
			return errors.New("plugin notification interrupted")
		}
		if !n.limiter.Allow(time.Now().UTC()) {
			continue
		}
		locale := n.sender.ResolveRecipientLocale(ctx, 0, recipient)
		message := notice.Message.Text(locale)
		if message == "" {
			message = notice.Message.Text("en")
		}
		if message == "" {
			message = notice.Message.Text("zh")
		}
		if message == "" {
			if notice.NativeRecovered {
				message = "Plugin credential maintenance restored the upstream account."
			} else if resolved {
				message = "Plugin credential maintenance marked the credential available."
			} else if notice.Status == "auto_disabled" {
				message = "Plugin credential maintenance temporarily disabled the upstream account."
			} else {
				message = "Plugin credential maintenance reported an update."
			}
		}
		err = n.sender.Send(ctx, NotificationEmailSendInput{Event: NotificationEmailEventOpsAlert, Locale: locale, RecipientEmail: recipient, RecipientName: emailRecipientName(recipient), SourceType: "plugin_credential", SourceID: notice.Identity,
			Variables: map[string]string{"rule_name": fmt.Sprintf("Plugin maintenance: %s (#%d)", notice.AccountName, notice.AccountID), "severity": opsEmailSeverityForOps(severity), "alert_status": notice.Status, "metric_type": "plugin_credential", "triggered_at": time.Now().UTC().Format(time.RFC3339), "alert_description": fmt.Sprintf("%s · %s", notice.PluginKey, message)}})
		if err != nil {
			failed = true
		}
	}
	if failed {
		return errors.New("plugin notification delivery failed")
	}
	return nil
}
