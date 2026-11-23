package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/officialupload"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/stretchr/testify/require"
)

type generationPreparationFixture struct {
	GenerationPreparationRepository
	GenerationTaskRepository
	*generationMultiFileFixture
	sealed      string
	checkpoints int
	saved       bool
	failSave    bool
}

func (r *generationPreparationFixture) CheckpointPreparation(_ context.Context, task *GenerationTask) error {
	if r.failSave {
		return ErrGenerationLeaseLost
	}
	r.sealed = task.PreparationEncrypted
	r.checkpoints++
	return nil
}

func (r *generationPreparationFixture) SaveProgress(_ context.Context, _ *GenerationTask, _ time.Duration) error {
	r.saved = true
	return nil
}

type generationUploadHTTP struct {
	HTTPUpstream
	do func(*http.Request) (*http.Response, error)
}

func (h *generationUploadHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return h.do(req)
}

func uploadResponse(value any) *http.Response {
	raw, _ := json.Marshal(map[string]any{"code": 0, "data": value})
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}
}

func officialUploadFixture(t *testing.T) (*NewAPIPluginService, *Account, *GenerationTask, *generationPreparationFixture) {
	t.Helper()
	s, account, _ := generationPluginFixture(t, "sub2api-official5")
	files := &generationMultiFileFixture{}
	file, err := files.StoreGenerationFile(context.Background(), 1, 2, 3, "private.png", "image/png", strings.NewReader("private-image-bytes"))
	require.NoError(t, err)
	repo := &generationPreparationFixture{generationMultiFileFixture: files}
	s.files, s.preparations, s.encryptor = files, repo, &plainEncryptor{}
	meta := GenerationFileSnapshot{ID: file.ID, SHA256: file.SHA256, File: pluginTask.File{Ref: "request_file:input_reference", Field: "input_reference", Filename: file.Filename, MimeType: file.MimeType, Size: file.Size}}
	ctx := context.WithValue(context.Background(), generationDecodeFilesKey{}, []GenerationFileSnapshot{meta})
	request, action, err := s.DecodeVideo(ctx, account, 1, "cc-seedance-fixture", json.RawMessage(`{"model":"cc-seedance-fixture","prompt":"Reference image","seconds":"8"}`))
	require.NoError(t, err)
	task := &GenerationTask{ID: "video_" + strings.Repeat("1", 40), UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: account.ID, PluginVersionID: 1, Model: "cc-seedance-fixture", UpstreamModel: "cc-seedance-fixture", Action: action, Request: request, Files: []GenerationFileSnapshot{meta}, State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`), Usage: json.RawMessage(`{}`), CreatedAt: time.Now(), Status: GenerationSubmitting, BillingStatus: GenerationBillingHeld, Connection: GenerationConnection{BaseURL: account.GetCredential("base_url"), Kind: "vendor"}}
	require.NoError(t, s.initializeTaskPreparation(ctx, task))
	require.Equal(t, "pending", task.PreparationStatus)
	return s, account, task, repo
}

func TestOfficialFiveActualWorkerUploadsBeforeSinglePaidSubmit(t *testing.T) {
	s, account, task, repo := officialUploadFixture(t)
	var relay, submit, complete int
	s.gateway.httpUpstream = &generationUploadHTTP{do: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "api.example.com", req.URL.Host)
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		require.Equal(t, "Bearer fixture-key", req.Header.Get("Authorization"))
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		switch req.URL.Path {
		case officialupload.Root:
			var upload officialupload.File
			require.NoError(t, json.Unmarshal(body, &upload))
			require.Equal(t, task.Files[0].SHA256, upload.SHA256)
			require.NotContains(t, string(body), "private.png")
			require.Contains(t, upload.IdempotencyKey, "sub2api-")
			return uploadResponse(map[string]any{"disposition": "owner", "upload": officialupload.Upload{Session: "private-session", Generation: 2, Mode: "single"}}), nil
		case officialupload.Root + "/relay":
			relay++
			require.GreaterOrEqual(t, repo.checkpoints, 2)
			require.Equal(t, "private-image-bytes", string(body))
			require.EqualValues(t, len(body), req.ContentLength)
			require.Equal(t, "private-session", req.Header.Get("X-Upload-Session"))
			return uploadResponse(map[string]any{}), nil
		case officialupload.Root + "/complete":
			complete++
			return uploadResponse(officialupload.Reference{ObjectID: "bound-object", Kind: "image", SHA256: task.Files[0].SHA256, Size: task.Files[0].Size}), nil
		case "/api/v1/video-generations":
			submit++
			require.Equal(t, 1, complete)
			require.Contains(t, string(body), `"storage_object_id":"bound-object"`)
			require.NotContains(t, string(body), "file_ref")
			require.NotContains(t, string(body), "duration_seconds")
			require.NotContains(t, string(body), "private-image-bytes")
			require.NotContains(t, string(body), "private-session")
			require.Equal(t, task.ID, req.Header.Get("X-Request-ID"))
			return uploadResponse(map[string]any{"id": "paid-once"}), nil
		default:
			t.Fatalf("unexpected request %s", req.URL.String())
			return nil, nil
		}
	}}
	// Calling submit before the durable preparation phase cannot reach HTTP.
	err := s.SubmitTask(context.Background(), account, task)
	var rejected *GenerationSubmissionError
	require.ErrorAs(t, err, &rejected)
	require.False(t, rejected.Uncertain)
	require.NoError(t, s.prepareTaskFiles(context.Background(), account, task))
	require.Zero(t, submit)
	require.Equal(t, 1, relay)
	// Restore only the durable checkpoint, as after a process restart.
	task.PreparationEncrypted = repo.sealed
	require.NoError(t, s.prepareTaskFiles(context.Background(), account, task))
	require.Equal(t, 1, relay)
	task.PreparationStatus = "ready"
	require.NoError(t, s.SubmitTask(context.Background(), account, task))
	require.Equal(t, 1, submit)
	require.Equal(t, "paid-once", task.UpstreamID)
	view, err := json.Marshal(task.VideoView())
	require.NoError(t, err)
	for _, private := range []string{"fixture-key", "private-session", "bound-object", "private.png", task.Files[0].ID, task.Files[0].Ref} {
		require.NotContains(t, string(view), private)
	}
}

func TestOfficialFiveUploadRejectsCorruptionForeignOwnershipAndChangedCredential(t *testing.T) {
	for _, test := range []string{"corrupt", "foreign-owner", "changed-size", "source-claim", "lease-lost"} {
		t.Run(test, func(t *testing.T) {
			s, account, task, repo := officialUploadFixture(t)
			requests := 0
			s.gateway.httpUpstream = &generationUploadHTTP{do: func(*http.Request) (*http.Response, error) { requests++; return nil, errors.New("must not call") }}
			switch test {
			case "corrupt":
				repo.data[task.Files[0].ID] = []byte("corrupted-image!!!")
			case "foreign-owner":
				task.UserID = 9
			case "changed-size":
				file := repo.files[task.Files[0].ID]
				file.Size++
				repo.files[file.ID] = file
			case "source-claim":
				s.repo.(*generationPluginFixtureRepo).version.Source += "\n// unreviewed source"
			case "lease-lost":
				repo.failSave = true
			}
			require.Error(t, s.prepareTaskFiles(context.Background(), account, task))
			require.Zero(t, requests)
		})
	}
	s, account, task, _ := officialUploadFixture(t)
	s.gateway.httpUpstream = &generationUploadHTTP{do: func(req *http.Request) (*http.Response, error) {
		return uploadResponse(map[string]any{"disposition": "reused", "reference": officialupload.Reference{ObjectID: "bound", Kind: "image", SHA256: task.Files[0].SHA256, Size: task.Files[0].Size}}), nil
	}}
	require.NoError(t, s.prepareTaskFiles(context.Background(), account, task))
	task.PreparationStatus = "ready"
	account.Credentials["api_key"] = "rotated-key"
	err := s.SubmitTask(context.Background(), account, task)
	var submission *GenerationSubmissionError
	require.ErrorAs(t, err, &submission)
	require.False(t, submission.Uncertain)
	task.PreparationStatus = "pending"
	var permanent *officialupload.Permanent
	require.ErrorAs(t, s.prepareTaskFiles(context.Background(), account, task), &permanent)
}

func TestOfficialFiveFailedUploadRetainsOriginalCleanupCredential(t *testing.T) {
	s, account, task, _ := officialUploadFixture(t)
	s.gateway.httpUpstream = &generationUploadHTTP{do: func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == officialupload.Root {
			return uploadResponse(map[string]any{"disposition": "owner", "upload": officialupload.Upload{Session: "abort-session", Generation: 1, Mode: "single"}}), nil
		}
		return nil, errors.New("connection interrupted")
	}}
	require.Error(t, s.prepareTaskFiles(context.Background(), account, task))
	account.Credentials["api_key"] = "new-key"
	task.PreparationStatus = "cleanup"
	calls := 0
	s.gateway.httpUpstream = &generationUploadHTTP{do: func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, officialupload.Root+"/abort", req.URL.Path)
		require.Equal(t, "Bearer fixture-key", req.Header.Get("Authorization"))
		return uploadResponse(map[string]any{}), nil
	}}
	require.NoError(t, s.cleanupTaskPreparation(context.Background(), account, task))
	require.NoError(t, s.cleanupTaskPreparation(context.Background(), account, task))
	require.Equal(t, 1, calls)
}

func TestOfficialFiveUploadPreparationWorkerRetriesBeforeSubmission(t *testing.T) {
	s, account, task, repo := officialUploadFixture(t)
	account.Status, account.Schedulable = "active", true
	s.gateway.concurrencyService = NewConcurrencyService(nil)
	worker := &GenerationService{repo: repo, plugins: s, accounts: channelAccountFixture{account: account}, gateway: s.gateway}
	calls := 0
	s.gateway.httpUpstream = &generationUploadHTTP{do: func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, officialupload.Root, req.URL.Path)
		return nil, errors.New("temporary outage")
	}}
	require.NoError(t, worker.process(context.Background(), task))
	require.True(t, repo.saved)
	require.Equal(t, GenerationQueued, task.Status)
	require.Equal(t, "pending", task.PreparationStatus)
	require.Equal(t, "upload_retrying", task.ErrorCode)
	require.Equal(t, 1, calls)
	task.Status = GenerationSubmitting
	task.CreatedAt = time.Now().Add(-16 * time.Minute)
	require.NoError(t, worker.process(context.Background(), task))
	require.Equal(t, GenerationFailed, task.Status)
	require.Equal(t, GenerationBillingPending, task.BillingStatus)
	require.Equal(t, "cleanup", task.PreparationStatus)
	require.NotNil(t, task.FinishedAt)
}

func TestOfficialFiveMultipartDurationAndReferenceIdentity(t *testing.T) {
	s, account, _ := generationPluginFixture(t, "sub2api-official5")
	file := GenerationFileSnapshot{File: pluginTask.File{Ref: "request_file:video[]", Field: "video[]", MimeType: "video/mp4", Size: 20}}
	ctx := context.WithValue(context.Background(), generationDecodeFilesKey{}, []GenerationFileSnapshot{file})
	_, _, err := s.DecodeVideo(ctx, account, 1, "cc-seedance-fixture", json.RawMessage(`{"prompt":"Video reference"}`))
	require.Error(t, err)
	request, _, err := s.DecodeVideo(ctx, account, 1, "cc-seedance-fixture", json.RawMessage(`{"prompt":"Video reference","metadata":{"reference_durations":{"video[]":[2.75]}}}`))
	require.NoError(t, err)
	require.Contains(t, string(request), `"duration_seconds":2.75`)
	for _, content := range []string{
		`[{"type":"image_url","storage_object_id":"a","client_ref_id":"same"},{"type":"image_url","storage_object_id":"b","client_ref_id":"same"}]`,
		`[{"type":"image_url","file_ref":"request_file:input_reference"}]`,
	} {
		_, _, err = s.DecodeVideo(context.Background(), account, 1, "cc-seedance-fixture", json.RawMessage(fmt.Sprintf(`{"prompt":"ref","metadata":{"content":%s}}`, content)))
		require.Error(t, err)
	}
	request, _, err = s.DecodeVideo(context.Background(), account, 1, "cc-seedance-fixture", json.RawMessage(`{"prompt":"ref","metadata":{"content":[{"type":"image_url","storage_object_id":"a","client_ref_id":"img1"}],"selected_reference_ids":["img1"]}}`))
	require.NoError(t, err)
	require.Contains(t, string(request), `"selected_reference_ids":["img1"]`)
}
