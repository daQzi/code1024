//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type nativeMaintenanceRepo struct {
	GenerationTaskRepository
	task           *NativeVideoMaintenance
	called         int
	action, reason string
}

func (r *nativeMaintenanceRepo) GetNativeVideoMaintenance(context.Context, string) (*NativeVideoMaintenance, error) {
	return r.task, nil
}
func (r *nativeMaintenanceRepo) ResumeNativeVideo(_ context.Context, _ string, _ int64, action, reason, _ string) error {
	r.called++
	r.action, r.reason = action, reason
	return nil
}

func TestNativeVideoMaintenanceOnlyRetriesOriginalWork(t *testing.T) {
	for _, action := range []string{"resume_poll", "retry_settlement"} {
		for _, scenario := range []string{"success", "invalid action", "short reason", "long reason", "invalid actor", "wrong task", "active", "failed", "changed connection", "disabled", "invalid funding", "missing units"} {
			t.Run(action+"/"+scenario, func(t *testing.T) {
				pollService, pollRepo, accounts, _, _ := nativePollFixture(t, GenerationProtocolXAI)
				_, settled, _ := nativeSettlementFixture(t, GenerationProtocolXAI)
				settled.Pending.Financial.TaskID = pollRepo.task.Observation.UpstreamID
				task := &NativeVideoMaintenance{Observation: pollRepo.task.Observation, Pending: settled.Pending, Usage: settled.Usage,
					ConnectionHash: pollRepo.task.ConnectionHash, MonitorStatus: NativeVideoMonitorReview, SettlementStatus: "awaiting_usage"}
				if action == "retry_settlement" {
					task.Observation.Status, task.Observation.DeliveryStatus = GenerationSucceeded, GenerationDeliveryReady
					task.MonitorStatus, task.SettlementStatus = NativeVideoMonitorComplete, "review"
				}
				repo := &nativeMaintenanceRepo{task: task}
				svc := &GenerationService{repo: repo, accounts: accounts, gateway: pollService.gateway}
				id, actor, chosen, reason := NativeVideoTaskID(task.Observation), int64(1), action, "  upstream checked  "
				wantError := scenario != "success"
				switch scenario {
				case "invalid action":
					chosen = "resubmit"
				case "short reason":
					reason = "   "
				case "long reason":
					reason = strings.Repeat("x", 2001)
				case "invalid actor":
					actor = 0
				case "wrong task":
					id = "native_video_" + strings.Repeat("b", 64)
				case "active":
					task.MonitorStatus, task.SettlementStatus = NativeVideoMonitorActive, "ready"
				case "failed":
					task.Observation.Status, task.Observation.DeliveryStatus = GenerationFailed, "failed"
				case "changed connection":
					accounts.account.Credentials["base_url"] = "https://different.example"
					wantError = action == "resume_poll"
				case "disabled":
					accounts.account.Status = StatusDisabled
					wantError = action == "resume_poll"
				case "invalid funding":
					task.Pending.Financial.UserID++
				case "missing units":
					task.Usage = nil
					wantError = action == "retry_settlement"
				}
				err := svc.MaintainNativeVideo(context.Background(), id, actor, chosen, reason)
				if wantError {
					require.Error(t, err)
					require.Zero(t, repo.called)
				} else {
					require.NoError(t, err)
					require.Equal(t, 1, repo.called)
					require.Equal(t, action, repo.action)
					require.Equal(t, "upstream checked", repo.reason)
				}
			})
		}
	}
}
