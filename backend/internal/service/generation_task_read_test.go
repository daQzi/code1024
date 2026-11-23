package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerationTaskArtifactListingAndContentUsePinnedPlugin(t *testing.T) {
	plugins, account, upstream := generationPluginFixture(t, "xai")
	ctx := context.Background()
	plugins.gateway.concurrencyService = NewConcurrencyService(nil)
	svc := &GenerationService{accounts: &stubOpenAIAccountRepo{accounts: []Account{*account}}, gateway: plugins.gateway, plugins: plugins}
	task := &GenerationTask{
		ID: "video_" + strings.Repeat("1", 40), AccountID: account.ID, PluginVersionID: 1, Status: GenerationSucceeded,
		UpstreamID: "private-upstream", State: json.RawMessage(`{}`), Data: json.RawMessage(`{"status":"done","video":{"url":"https://vidgen.x.ai/private.mp4"}}`),
		Connection: GenerationConnection{BaseURL: "https://api.example.com", Kind: "vendor"},
	}
	items, err := svc.ListTaskArtifacts(ctx, task)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "video", items[0].Key)
	require.Equal(t, "video/mp4", items[0].MimeType)
	require.Empty(t, upstream.requests, "listing uses only persisted state")
	_, _, err = svc.TaskArtifactContent(ctx, task, http.MethodGet, "other", nil)
	require.ErrorIs(t, err, ErrGenerationArtifactNotFound)
	require.Empty(t, upstream.requests)
	upstream.response, upstream.contentType, upstream.status = "video-bytes", "video/mp4", http.StatusPartialContent
	response, release, err := svc.TaskArtifactContent(ctx, task, http.MethodGet, "video", http.Header{"Range": {"bytes=0-10"}, "Authorization": {"Bearer private-downstream-key"}})
	require.NoError(t, err)
	defer release()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "video-bytes", string(data))
	require.Equal(t, http.StatusPartialContent, response.StatusCode)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://vidgen.x.ai/private.mp4", upstream.requests[0].URL.String())
	require.Equal(t, "bytes=0-10", upstream.requests[0].Header.Get("Range"))
	require.Empty(t, upstream.requests[0].Header.Get("Authorization"))
}

func TestGenerationTaskArtifactListingRejectsMalformedOrNetworkedProjection(t *testing.T) {
	for _, hook := range []string{
		`return [{key:"../private",type:"video"}];`,
		`return [{key:"video",type:"video"},{key:"video",type:"file"}];`,
		`utils.fetch({url:"https://api.example.com/artifacts"}); return [];`,
	} {
		t.Run(hook, func(t *testing.T) {
			plugins, account, upstream := generationPluginFixture(t, "sora")
			version := plugins.repo.(*generationPluginFixtureRepo).version
			version.Source = strings.Replace(version.Source, `return task.status === "SUCCESS" ? [{ key: "video", type: "video" }] : [];`, hook, 1)
			svc := &GenerationService{accounts: &stubOpenAIAccountRepo{accounts: []Account{*account}}, plugins: plugins}
			task := &GenerationTask{AccountID: account.ID, PluginVersionID: 1, Status: GenerationSucceeded, State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`)}
			_, err := svc.ListTaskArtifacts(context.Background(), task)
			require.Error(t, err)
			require.Empty(t, upstream.requests)
		})
	}
}
