package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTaskStoredFileLateIngressKeepsBindingAndHidesIDsFromReport(t *testing.T) {
	source := strings.Replace(taskEgressSource, "apiVersion:1,", "apiVersion:1,scope:{channelTypes:[61]},", 1) + `
export function report(){}
export function ingress(ctx,req){
 const f=utils.attachments()[0];
 if(ctx.channel.type!==61 || f.path!=="input_reference" || f.size!==8 || f.mime!=="image/png" || req.body.file_references!==undefined || utils.mediaText(f.ref).unchecked!=="reference")throw Error("file projection");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/seconds",value:"4"}]}]};
}`
	s, repo, account, _ := taskEgressFixture(t, source)
	account.Credentials[NewAPIVideoPricingKey] = map[string]any{"*": map[string]any{"mode": "fixed", "unit": "second", "unit_price": "0.02"}}
	repo.items[0].Stages = []string{"ingress", "report"}
	s.requestPlugins.expires = time.Time{}
	call := RequestPluginCall{UserID: 1, TokenID: 2, GroupID: 3, Model: "sora-2", Format: "task", Path: "/v1/videos"}
	plan, err := s.NewRequestPluginPlan(context.Background(), call)
	require.NoError(t, err)
	require.False(t, plan.HasEarlyIngress())
	group := &Group{ID: 3, RateMultiplier: 1}
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, GroupID: &group.ID, Group: group}
	files := &generationMultiFileFixture{}
	file, err := files.StoreGenerationFile(context.Background(), 1, 2, 3, "reference.png", "image/png", strings.NewReader("\x89PNG\r\n\x1a\n"))
	require.NoError(t, err)
	held := &generationReplayFixture{}
	svc := &GenerationService{repo: &generationFileTaskRepo{GenerationTaskRepository: held, GenerationFileRepository: files}, plugins: s}
	body := json.RawMessage(`{"model":"sora-2","prompt":"Draw","seconds":"8","file_references":[ { "field":"input_reference", "file_id":"` + file.ID + `" } ]}`)
	ctx := WithRequestPluginPlan(context.Background(), plan)
	task, err := svc.Create(ctx, key, nil, account, "sora-2", body, "same-video")
	require.NoError(t, err, "runs: %+v", repo.runs)
	require.Equal(t, "0.08", task.Price.Hold.String())
	require.Len(t, task.Files, 1)
	require.Equal(t, file.ID, task.Files[0].ID)
	require.Equal(t, file.SHA256, task.Files[0].SHA256)
	require.Equal(t, 1, files.stored, "preupload is bound by reference without a second write")
	snapshot, err := s.openTaskReport(task)
	require.NoError(t, err)
	require.JSONEq(t, `null`, string(snapshot.Body))
	require.Len(t, snapshot.Records, 1)
	require.Len(t, snapshot.Records[0].Attachments, 1)
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), file.ID)
	require.NotContains(t, string(encoded), file.Filename)
}
