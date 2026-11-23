package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/stretchr/testify/require"
)

type generationMemoryFiles struct {
	GenerationFileRepository
	file GenerationFile
	data string
}

func (f *generationMemoryFiles) GetGenerationFile(_ context.Context, id string, user, key, group int64) (*GenerationFile, error) {
	if id != f.file.ID || user != 1 || key != 2 || group != 3 {
		return nil, ErrGenerationFileNotFound
	}
	return &f.file, nil
}
func (f *generationMemoryFiles) OpenGenerationFile(ctx context.Context, id string, user, key, group int64) (io.ReadCloser, error) {
	if _, err := f.GetGenerationFile(ctx, id, user, key, group); err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader(f.data)), nil
}

func (f *generationMemoryFiles) DeleteGenerationFile(_ context.Context, id string, user, key, group int64) error {
	if id != f.file.ID || user != 1 || key != 2 || group != 3 {
		return ErrGenerationFileNotFound
	}
	f.file, f.data = GenerationFile{}, ""
	return nil
}

func (f *generationMemoryFiles) StoreGenerationFile(_ context.Context, user, key, group int64, name, kind string, source io.ReadSeeker) (*GenerationFile, error) {
	if user != 1 || key != 2 || group != 3 {
		return nil, ErrGenerationFileNotFound
	}
	data, err := io.ReadAll(source)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	f.data = string(data)
	f.file = GenerationFile{ID: "vfile_" + strings.Repeat("a", 40), Filename: name, MimeType: kind, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
	return &f.file, nil
}

type generationFileHTTP struct {
	HTTPUpstream
	request *http.Request
	body    []byte
}

func (h *generationFileHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	h.request = req
	var err error
	h.body, err = io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"upstream-file-task"}`))}, nil
}

func TestGenerationFileSoraWorkerMultipartAndExactSignature(t *testing.T) {
	s, account, _ := generationPluginFixture(t, "sora")
	files := &generationMemoryFiles{file: GenerationFile{ID: "vfile_" + strings.Repeat("a", 40), Filename: "fixture.png", MimeType: "image/png", Size: 700000, SHA256: strings.Repeat("b", 64)}, data: strings.Repeat("x", 700000)}
	s.files = files
	s.repo.(*generationPluginFixtureRepo).version.Source += `\nexport function signRequest(ctx,req){return {headers:{"X-Body-Digest":req.bodySha256}};}`
	// Use a real newline in the additional source.
	s.repo.(*generationPluginFixtureRepo).version.Source = strings.ReplaceAll(s.repo.(*generationPluginFixtureRepo).version.Source, `\nexport`, "\nexport")
	httpClient := &generationFileHTTP{}
	s.gateway.httpUpstream = httpClient
	meta := GenerationFileSnapshot{ID: files.file.ID, SHA256: files.file.SHA256, File: pluginTask.File{Ref: "request_file:input_reference", Field: "input_reference", Filename: files.file.Filename, MimeType: files.file.MimeType, Size: files.file.Size}}
	ctx := context.WithValue(context.Background(), generationDecodeFilesKey{}, []GenerationFileSnapshot{meta})
	decoded, action, err := s.DecodeVideo(ctx, account, 1, "sora-2", json.RawMessage(`{"model":"sora-2","prompt":"ocean","seconds":"8"}`))
	require.NoError(t, err)
	require.Equal(t, "image_to_video", action)
	task := &GenerationTask{ID: "video_" + strings.Repeat("1", 40), UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: account.ID, PluginVersionID: 1, Model: "sora-2", UpstreamModel: "sora-2", Action: action, Request: decoded, Files: []GenerationFileSnapshot{meta}, CreatedAt: time.Now(), State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`)}
	require.NoError(t, s.SubmitTask(ctx, account, task))
	require.Equal(t, "upstream-file-task", task.UpstreamID)
	digest := sha256.Sum256(httpClient.body)
	require.Equal(t, hex.EncodeToString(digest[:]), httpClient.request.Header.Get("X-Body-Digest"))
	require.EqualValues(t, len(httpClient.body), httpClient.request.ContentLength)
	_, params, err := mime.ParseMediaType(httpClient.request.Header.Get("Content-Type"))
	require.NoError(t, err)
	reader := multipart.NewReader(bytes.NewReader(httpClient.body), params["boundary"])
	found := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if part.FileName() != "" {
			data, err := io.ReadAll(part)
			require.NoError(t, err)
			require.Equal(t, files.data, string(data))
			require.Equal(t, "input_reference", part.FormName())
			found = true
		}
	}
	require.True(t, found)
	// A plugin cannot use an arbitrary file ID or a different key's media.
	task.APIKeyID = 99
	err = s.SubmitTask(ctx, account, task)
	require.Error(t, err)
	var submission *GenerationSubmissionError
	require.ErrorAs(t, err, &submission)
	require.False(t, submission.Uncertain)
}

func TestGenerationFileDecodeAndSnapshotOwnership(t *testing.T) {
	files := &generationMemoryFiles{file: GenerationFile{ID: "vfile_" + strings.Repeat("a", 40), Filename: "in.png", MimeType: "image/png", Size: 3, SHA256: strings.Repeat("b", 64)}, data: "abc"}
	svc := &GenerationService{repo: &generationFileTaskRepo{GenerationFileRepository: files}}
	group := int64(3)
	key := &APIKey{ID: 2, UserID: 1, GroupID: &group}
	body := json.RawMessage(`{"model":"sora-2","file_references":[{"file_id":"` + files.file.ID + `","field":"input_reference"}]}`)
	cleaned, refs, err := svc.prepareVideoFiles(context.Background(), key, body, GenerationProtocolOpenAI)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"sora-2"}`, string(cleaned))
	require.Len(t, refs, 1)
	key.ID = 99
	_, _, err = svc.prepareVideoFiles(context.Background(), key, body, GenerationProtocolOpenAI)
	require.ErrorIs(t, err, ErrGenerationFileNotFound)
	key.ID = 2
	_, _, err = svc.prepareVideoFiles(context.Background(), key, body, GenerationProtocolXAI)
	require.Error(t, err)
	for _, name := range []string{"../private", "bad\r\nname", strings.Repeat("x", 256)} {
		_, _, err := ValidateGenerationFilename(name, "image/png")
		require.Error(t, err)
	}
}

func TestGenerationFileMultipartInputPersistsOpaqueReference(t *testing.T) {
	files := &generationMemoryFiles{}
	svc := &GenerationService{repo: &generationFileTaskRepo{GenerationFileRepository: files}}
	group := int64(3)
	key := &APIKey{ID: 2, UserID: 1, GroupID: &group}
	body := json.RawMessage(`{"model":"sora-2","input_reference":"data:image/png;base64,YWJj"}`)
	unchanged, refs, err := svc.prepareVideoFiles(context.Background(), key, body, GenerationProtocolOpenAI)
	require.NoError(t, err)
	require.Empty(t, refs)
	require.Equal(t, body, unchanged)
	cleaned, refs, err := svc.prepareVideoFiles(WithGenerationMultipartInput(context.Background()), key, body, GenerationProtocolOpenAI)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"sora-2"}`, string(cleaned))
	require.Len(t, refs, 1)
	require.Equal(t, "abc", files.data)
	wire, err := generationFileDecodeBody(cleaned, refs)
	require.NoError(t, err)
	require.Equal(t, "multipart", wire["kind"])
}

type generationFileTaskRepo struct {
	GenerationTaskRepository
	GenerationFileRepository
}

func TestGenerationFileSpoolCancellationCapacityAndReplay(t *testing.T) {
	files := &generationMemoryFiles{}
	svc := &GenerationService{repo: &generationFileTaskRepo{GenerationFileRepository: files}}
	group := int64(3)
	key := &APIKey{ID: 2, UserID: 1, GroupID: &group, User: &User{ID: 1}, Group: &Group{ID: 3}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.UploadGenerationFile(ctx, key, "in.png", "image/png", strings.NewReader("abc"))
	require.ErrorIs(t, err, context.Canceled)
	for range cap(generationUploadSlots) {
		generationUploadSlots <- struct{}{}
	}
	_, err = svc.UploadGenerationFile(context.Background(), key, "in.png", "image/png", strings.NewReader("abc"))
	for range cap(generationUploadSlots) {
		<-generationUploadSlots
	}
	require.ErrorIs(t, err, ErrGenerationFileBusy)
	spool, err := newGenerationSpool()
	require.NoError(t, err)
	_, err = os.Stat(spool.Name())
	require.True(t, os.IsNotExist(err), "temporary media is immediately unlinked")
	_, err = spool.WriteString("still open")
	require.NoError(t, err)
	require.NoError(t, spool.Close())
	body := json.RawMessage(`{"model":"sora-2","file_references":[{"file_id":"vfile_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","field":"input_reference"}]}`)
	fingerprint, err := generationRequestFingerprint(body)
	require.NoError(t, err)
	task := &GenerationTask{UserID: 1, APIKeyID: 2, IdempotencyKey: "accepted", Fingerprint: fingerprint}
	svc = &GenerationService{repo: &generationReplayFixture{task: task}} // File has expired and plugins/pricing no longer exist.
	replayed, err := svc.Create(context.Background(), key, nil, &Account{}, "sora-2", body, "accepted")
	require.NoError(t, err)
	require.Same(t, task, replayed)
}

func TestGenerationStreamingMultipartSoraIdentityAndCleanup(t *testing.T) {
	plugins, account, upstream := generationPluginFixture(t, "sora")
	account.Credentials[NewAPIVideoPricingKey] = map[string]any{"*": map[string]any{"mode": "fixed", "unit": "second", "unit_price": "0.02"}}
	files := &generationMemoryFiles{}
	replay := &generationReplayFixture{}
	svc := &GenerationService{repo: &generationFileTaskRepo{GenerationTaskRepository: replay, GenerationFileRepository: files}, plugins: plugins}
	group := int64(3)
	key := &APIKey{ID: 2, UserID: 1, GroupID: &group, User: &User{ID: 1}, Group: &Group{ID: group, RateMultiplier: 1}}
	image := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 2<<20)...)
	makeBody := func(name string, content []byte, reverse bool) (string, []byte) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if !reverse {
			require.NoError(t, writer.WriteField("model", "sora-2"))
		}
		require.NoError(t, writer.WriteField("prompt", "ocean"))
		require.NoError(t, writer.WriteField("seconds", "8"))
		part, err := writer.CreateFormFile("input_reference", name)
		require.NoError(t, err)
		_, err = part.Write(content)
		require.NoError(t, err)
		if reverse {
			require.NoError(t, writer.WriteField("model", "sora-2"))
		}
		require.NoError(t, writer.Close())
		return writer.FormDataContentType(), body.Bytes()
	}
	kind, body := makeBody("first.png", image, false)
	ctx, normalized, cleanup, err := svc.ReadVideoMultipart(context.Background(), key, kind, bytes.NewReader(body))
	require.NoError(t, err)
	require.Empty(t, files.file.ID, "spooling must not consume database quota before replay")
	require.JSONEq(t, `{"model":"sora-2","prompt":"ocean","seconds":"8"}`, string(normalized))
	task, err := svc.Create(ctx, key, nil, account, "sora-2", normalized, "stream-once")
	require.NoError(t, err)
	require.Equal(t, "image_to_video", task.Action)
	require.Len(t, task.Files, 1)
	require.Equal(t, image, []byte(files.data))
	require.Equal(t, "0.16", task.Price.Hold.String())
	require.NotContains(t, string(task.Request), "data:")
	require.Less(t, len(task.Request), 1024)
	require.Empty(t, upstream.requests)
	cleanup() // The fake has no bindings; the real SQL binding prevents deletion.
	require.Empty(t, files.file.ID)
	replay.task = task
	svc.plugins = nil // A retry works even after the plugin disappears.
	kind, body = makeBody("renamed.png", image, true)
	ctx, normalized, cleanup, err = svc.ReadVideoMultipart(context.Background(), key, kind, bytes.NewReader(body))
	require.NoError(t, err)
	// Ingress policy must not replace the file's original replay fingerprint.
	ctx, err = WithGenerationClientRequest(ctx, json.RawMessage(`{"model":"sora-2","prompt":"new policy"}`))
	require.NoError(t, err)
	got, err := svc.Create(ctx, key, nil, account, "sora-2", normalized, "stream-once")
	require.NoError(t, err)
	require.Same(t, task, got)
	require.Empty(t, files.file.ID)
	cleanup()
	image[len(image)-1] = 'y'
	kind, body = makeBody("renamed.png", image, true)
	ctx, normalized, cleanup, err = svc.ReadVideoMultipart(context.Background(), key, kind, bytes.NewReader(body))
	require.NoError(t, err)
	_, err = svc.Create(ctx, key, nil, account, "sora-2", normalized, "stream-once")
	require.ErrorIs(t, err, ErrGenerationIdempotencyConflict)
	cleanup()
	// Old small-file identity survives the new streamed parser.
	kind, body = makeBody("small.png", image[:100], false)
	original, err := pluginTask.NormalizeVideoRequest(kind, body)
	require.NoError(t, err)
	want, err := generationRequestFingerprint(original)
	require.NoError(t, err)
	ctx, normalized, cleanup, err = svc.ReadVideoMultipart(context.Background(), key, kind, bytes.NewReader(body))
	require.NoError(t, err)
	actual, err := generationClientFingerprint(ctx, normalized)
	require.NoError(t, err)
	require.Equal(t, want, actual)
	cleanup()
}

func TestGenerationStreamingMultipartLimitsAndAdmission(t *testing.T) {
	svc := &GenerationService{}
	for _, tc := range []struct {
		name  string
		write func(*multipart.Writer)
		want  error
	}{
		{"oversized image", func(writer *multipart.Writer) {
			part, err := writer.CreateFormFile("input_reference", "input.png")
			require.NoError(t, err)
			_, err = io.CopyN(part, strings.NewReader(strings.Repeat("x", (32<<20)+1)), (32<<20)+1)
			require.NoError(t, err)
		}, ErrGenerationFileTooLarge},
		{"oversized text", func(writer *multipart.Writer) {
			require.NoError(t, writer.WriteField("prompt", strings.Repeat("x", (512<<10)+1)))
		}, pluginTask.ErrVideoMultipartTooLarge},
		{"duplicate model", func(writer *multipart.Writer) {
			require.NoError(t, writer.WriteField("model", "one"))
			require.NoError(t, writer.WriteField("model", "two"))
		}, nil},
		{"unknown file", func(writer *multipart.Writer) {
			_, err := writer.CreateFormFile("other", "input.png")
			require.NoError(t, err)
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			tc.write(writer)
			require.NoError(t, writer.Close())
			_, _, cleanup, err := svc.ReadVideoMultipart(context.Background(), nil, writer.FormDataContentType(), bytes.NewReader(body.Bytes()))
			require.Error(t, err)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			}
			require.Nil(t, cleanup)
			require.Empty(t, generationUploadSlots, "failed parsing releases upload admission")
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := svc.ReadVideoMultipart(canceled, nil, "", strings.NewReader(""))
	require.ErrorIs(t, err, context.Canceled)
	for range cap(generationUploadSlots) {
		generationUploadSlots <- struct{}{}
	}
	_, _, _, err = svc.ReadVideoMultipart(context.Background(), nil, "", strings.NewReader(""))
	for range cap(generationUploadSlots) {
		<-generationUploadSlots
	}
	require.ErrorIs(t, err, ErrGenerationFileBusy)
}

func BenchmarkGenerationMultipartStreaming(b *testing.B) {
	for _, count := range []int{1, pluginTask.MaxTaskFiles} {
		for _, size := range []int{2 << 20, 32 << 20} {
			b.Run(fmt.Sprintf("%dfiles-%dMiB", count, size>>20), func(b *testing.B) {
				var body bytes.Buffer
				body.Grow(count*size + 16384)
				writer := multipart.NewWriter(&body)
				if err := writer.WriteField("model", "sora-2"); err != nil {
					b.Fatal(err)
				}
				for range count {
					part, err := writer.CreateFormFile("input_reference", "fixture.png")
					if err != nil {
						b.Fatal(err)
					}
					if _, err = part.Write(append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), size-8)...)); err != nil {
						b.Fatal(err)
					}
				}
				if err := writer.Close(); err != nil {
					b.Fatal(err)
				}
				svc := &GenerationService{}
				b.ReportAllocs()
				b.SetBytes(int64(size * count))
				b.ResetTimer()
				for range b.N {
					_, _, cleanup, err := svc.ReadVideoMultipart(context.Background(), nil, writer.FormDataContentType(), bytes.NewReader(body.Bytes()))
					if err != nil {
						b.Fatal(err)
					}
					cleanup()
				}
			})
		}
	}
}
