package service

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTaskMultipartLateIngressBeforeStorageAndHold(t *testing.T) {
	for _, scenario := range []string{"accepted", "rejected", "audit unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			source := strings.Replace(taskEgressSource, "apiVersion:1,", "apiVersion:1,scope:{channelTypes:[61]},", 1) + `
export function ingress(ctx,req){
 const files=utils.attachments();
 const file=files[0];
 if(ctx.channel.type!==61 || ctx.upstreamModel!=="sora-2" || req.body.seconds!=="8" || req.body.input_reference!==undefined || files.length!==1 || file.path!=="input_reference" || file.size!==600008 || file.mime!=="image/png")throw Error("wrong Task file or channel");
 if(ctx.config.deny==="reject")return {action:"reject",code:"blocked"};
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/seconds",value:"4"},{op:"replace",path:"/prompt",value:"Approved"}]}]};
}`
			s, repo, account, _ := taskEgressFixture(t, source)
			account.Credentials[NewAPIVideoPricingKey] = map[string]any{"*": map[string]any{"mode": "fixed", "unit": "second", "unit_price": "0.02"}}
			repo.items[0].Stages = []string{"ingress"}
			if scenario == "rejected" {
				repo.items[0].ConfigEncrypted = `ENC:{"deny":"reject"}`
			}
			if scenario == "audit unavailable" {
				repo.runErr = io.ErrClosedPipe
			}
			s.requestPlugins.expires = time.Time{}
			call := RequestPluginCall{UserID: 4, TokenID: 5, GroupID: 7, Model: "sora-2", Format: "task", Path: "/v1/videos"}
			plan, err := s.NewRequestPluginPlan(context.Background(), call)
			require.NoError(t, err)
			require.False(t, plan.HasEarlyIngress())
			groupID := int64(7)
			key := &APIKey{ID: 5, UserID: 4, User: &User{ID: 4}, GroupID: &groupID, Group: &Group{ID: 7, RateMultiplier: 1}}
			held, files := &generationReplayFixture{}, &generationMultiFileFixture{}
			svc := &GenerationService{repo: &generationFileTaskRepo{GenerationTaskRepository: held, GenerationFileRepository: files}, plugins: s}
			contents := strings.Repeat("a", 600000)
			contentType, raw := multiFileRequest(t, []string{"input_reference"}, []string{contents})
			ctx, body, cleanup, err := svc.ReadVideoMultipart(WithRequestPluginPlan(context.Background(), plan), key, contentType, bytes.NewReader(raw))
			require.NoError(t, err)
			defer cleanup()
			task, err := svc.Create(ctx, key, nil, account, "sora-2", body, "same-video")
			if scenario != "accepted" {
				var failure *RequestPluginFailure
				require.ErrorAs(t, err, &failure)
				require.Equal(t, scenario == "rejected", failure.Rejected)
				require.Zero(t, files.stored)
				require.Nil(t, held.held)
				return
			}
			require.NoError(t, err, "runs: %+v", repo.runs)
			require.Equal(t, "0.08", task.Price.Hold.String())
			require.Equal(t, "Approved", task.PublicFields.Prompt)
			require.Equal(t, "4", task.PublicFields.Seconds)
			require.Equal(t, ctx.Value(generationClientFingerprintKey{}), task.Fingerprint)
			require.Len(t, task.Files, 1)
			require.Equal(t, "\x89PNG\r\n\x1a\n"+contents, string(files.data[task.Files[0].ID]))
			require.Len(t, repo.runs, 1)
			held.task = task
			repo.runErr = io.ErrClosedPipe
			replayed, err := svc.Create(ctx, key, nil, account, "sora-2", body, "same-video")
			require.NoError(t, err)
			require.Same(t, task, replayed)
			require.Equal(t, 1, files.stored)
			require.Len(t, repo.runs, 1)
		})
	}
}
