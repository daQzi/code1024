package service

import (
	"context"
	"encoding/json"
	"testing"

	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/stretchr/testify/require"
)

func TestGenerationStockAlibabaImageProtocol(t *testing.T) {
	plugins, account, upstream := generationPluginFixture(t, "alibaba")
	plugins.encryptor = pluginTokenEncryptor{}
	ctx := context.Background()
	account.Credentials["model_mapping"] = map[string]any{"public-image": "qwen-image-2.0"}
	require.NoError(t, plugins.NormalizeAccountBinding(ctx, account, nil))
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityPluginImageTask))
	require.True(t, account.SupportsNewAPIImageTaskModel("public-image"))
	require.False(t, account.SupportsNewAPIImageTaskModel("qwen-image-2.0"), "alias must not also expose the upstream name")
	require.False(t, IsVideoGenerationProtocol(GenerationProtocolOpenAIImage))
	body := json.RawMessage(`{"model":"public-image","prompt":"A blue sky","response_format":"b64_json"}`)
	gen, err := plugins.decodeVideoIntent(ctx, 1, "public-image", "qwen-image-2.0", body, GenerationProtocolOpenAIImage)
	require.NoError(t, err)
	require.Equal(t, "text_to_image", gen.Action)

	editCtx := WithGenerationIngressPath(ctx, "/v1/images/edits")
	_, err = plugins.decodeVideoIntent(editCtx, 1, "public-image", "qwen-image-2.0", body, GenerationProtocolOpenAIImage)
	require.Error(t, err, "the original plugin requires an image for edits")
	editCtx = context.WithValue(editCtx, generationDecodeFilesKey{}, []GenerationFileSnapshot{{
		File: pluginTask.File{Ref: "request_file:image", Field: "image", Filename: "input.png", Size: 10, MimeType: "image/png"},
	}})
	edit, err := plugins.decodeVideoIntent(editCtx, 1, "public-image", "qwen-image-2.0", body, GenerationProtocolOpenAIImage)
	require.NoError(t, err)
	require.Equal(t, "image_to_image", edit.Action)
	require.Contains(t, string(edit.RequestBody), `"__fileRef":"request_file:image"`)
	require.Empty(t, upstream.requests, "decoding does not reach the network")
}

func TestGenerationBoundTaskReplayKeepsGroupAndOperation(t *testing.T) {
	body := json.RawMessage(`{"model":"m1","prompt":"An ocean"}`)
	group := int64(3)
	key := &APIKey{ID: 2, UserID: 1, GroupID: &group, Group: &Group{ID: group}, User: &User{ID: 1}}
	protocol := GenerationProtocolOpenAIImage
	gen := WithGenerationIngressPath(context.Background(), "/v1/images/generations")
	edit := WithGenerationIngressPath(context.Background(), "/v1/images/edits")
	fingerprint, err := generationClientFingerprint(withGenerationProtocol(gen, protocol), body)
	require.NoError(t, err)
	task := &GenerationTask{UserID: 1, APIKeyID: 2, GroupID: group, Protocol: protocol, IdempotencyKey: "once", Fingerprint: fingerprint}
	svc := &GenerationService{repo: &generationReplayFixture{task: task}}
	got, err := svc.ReplayBoundTask(gen, key, "once", body, protocol)
	require.NoError(t, err)
	require.Same(t, task, got)
	_, err = svc.ReplayBoundTask(edit, key, "once", body, protocol)
	require.ErrorIs(t, err, ErrGenerationIdempotencyConflict)
	group++
	key.Group.ID = group
	_, err = svc.ReplayBoundTask(gen, key, "once", body, protocol)
	require.ErrorIs(t, err, ErrGenerationIdempotencyConflict)
	_, err = svc.CreateVideo(gen, key, nil, &Account{}, "m1", body, "once", protocol)
	require.ErrorIs(t, err, ErrGenerationIdempotencyConflict)
}
