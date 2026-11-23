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
	"os"
	"strings"
	"testing"

	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/stretchr/testify/require"
)

type generationMultiFileFixture struct {
	GenerationFileRepository
	files  map[string]GenerationFile
	data   map[string][]byte
	stored int
	failAt int
}

func (r *generationMultiFileFixture) StoreGenerationFile(_ context.Context, user, key, group int64, name, kind string, source io.ReadSeeker) (*GenerationFile, error) {
	if r.failAt > 0 && r.stored+1 == r.failAt {
		return nil, ErrGenerationFileQuota
	}
	data, err := io.ReadAll(source)
	if err != nil {
		return nil, err
	}
	r.stored++
	id := fmt.Sprintf("vfile_%040x", r.stored)
	digest := sha256.Sum256(data)
	file := GenerationFile{ID: id, Filename: name, MimeType: kind, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
	if r.files == nil {
		r.files = map[string]GenerationFile{}
		r.data = map[string][]byte{}
	}
	r.files[id], r.data[id] = file, data
	return &file, nil
}
func (r *generationMultiFileFixture) GetGenerationFile(_ context.Context, id string, user, key, group int64) (*GenerationFile, error) {
	file, found := r.files[id]
	if !found || user != 1 || key != 2 || group != 3 {
		return nil, ErrGenerationFileNotFound
	}
	return &file, nil
}
func (r *generationMultiFileFixture) OpenGenerationFile(ctx context.Context, id string, user, key, group int64) (io.ReadCloser, error) {
	if _, err := r.GetGenerationFile(ctx, id, user, key, group); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(r.data[id])), nil
}
func (r *generationMultiFileFixture) DeleteGenerationFile(_ context.Context, id string, user, key, group int64) error {
	delete(r.files, id)
	delete(r.data, id)
	return nil
}

func multiFileRequest(t *testing.T, fields, content []string) (string, []byte) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.WriteField("model", "sora-2"))
	require.NoError(t, w.WriteField("prompt", "ocean"))
	require.NoError(t, w.WriteField("seconds", "8"))
	for i, field := range fields {
		part, err := w.CreateFormFile(field, fmt.Sprintf("%d.png", i))
		require.NoError(t, err)
		_, err = io.WriteString(part, "\x89PNG\r\n\x1a\n"+content[i])
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return w.FormDataContentType(), body.Bytes()
}

func TestGenerationMultipleFilesStreamSnapshotReplayAndCleanup(t *testing.T) {
	ctx := context.Background()
	group := int64(3)
	key := &APIKey{UserID: 1, ID: 2, GroupID: &group}
	files := &generationMultiFileFixture{}
	svc := &GenerationService{repo: &generationFileTaskRepo{GenerationFileRepository: files}}
	kind, body := multiFileRequest(t, []string{"image[]", "reference_audio", "image[]"}, []string{"first", "audio", "last"})
	request, normalized, cleanup, err := svc.ReadVideoMultipart(ctx, key, kind, bytes.NewReader(body))
	require.NoError(t, err)
	require.Zero(t, files.stored, "stream read must not persist before idempotent replay")
	_, refs, err := svc.prepareVideoFiles(request, key, normalized, GenerationProtocolOpenAI)
	require.NoError(t, err)
	require.Len(t, refs, 3)
	require.Equal(t, []string{"image[]", "image[]", "reference_audio"}, []string{refs[0].Field, refs[1].Field, refs[2].Field})
	require.Equal(t, "request_file:image[]", refs[0].Ref)
	require.Equal(t, "request_file:image[]:1", refs[1].Ref)
	wire, err := generationFileDecodeBody(normalized, refs)
	require.NoError(t, err)
	require.Len(t, wire["files"], 3)
	hash, err := generationClientFingerprint(request, normalized)
	require.NoError(t, err)
	upload := request.Value(generationMultipartUploadKey{}).(*generationMultipartUpload)
	spools := make([]*os.File, 0, len(upload.files))
	for _, file := range upload.files {
		spools = append(spools, file.file)
	}
	cleanup()
	cleanup()
	require.Empty(t, files.files)
	require.Empty(t, generationUploadSlots)
	for _, file := range spools {
		_, err := file.Stat()
		require.Error(t, err, "all descriptors must close")
	}
	// Interleaving differently named file fields is not semantic; order within
	// image[] is semantic and must change the request fingerprint.
	for _, tc := range []struct {
		fields, values []string
		equal          bool
	}{
		{[]string{"reference_audio", "image[]", "image[]"}, []string{"audio", "first", "last"}, true},
		{[]string{"image[]", "image[]", "reference_audio"}, []string{"last", "first", "audio"}, false},
	} {
		kind, body = multiFileRequest(t, tc.fields, tc.values)
		request, normalized, done, err := svc.ReadVideoMultipart(ctx, key, kind, bytes.NewReader(body))
		require.NoError(t, err)
		got, err := generationClientFingerprint(request, normalized)
		require.NoError(t, err)
		require.Equal(t, tc.equal, hash == got)
		done()
	}
}

func TestGenerationMultipleFilePersistenceFailureAndReferences(t *testing.T) {
	group := int64(3)
	key := &APIKey{UserID: 1, ID: 2, GroupID: &group}
	files := &generationMultiFileFixture{failAt: 2}
	svc := &GenerationService{repo: &generationFileTaskRepo{GenerationFileRepository: files}}
	kind, body := multiFileRequest(t, []string{"input_reference", "input_reference"}, []string{"first", "last"})
	ctx, normalized, cleanup, err := svc.ReadVideoMultipart(context.Background(), key, kind, bytes.NewReader(body))
	require.NoError(t, err)
	_, _, err = svc.prepareVideoFiles(ctx, key, normalized, GenerationProtocolOpenAI)
	require.ErrorIs(t, err, ErrGenerationFileQuota)
	require.Len(t, files.files, 1)
	cleanup()
	require.Empty(t, files.files)
	files.failAt = 0
	file, err := files.StoreGenerationFile(context.Background(), 1, 2, 3, "same.png", "image/png", strings.NewReader("fixture"))
	require.NoError(t, err)
	request := json.RawMessage(`{"model":"sora-2","file_references":[{"file_id":"` + file.ID + `","field":"image[]"},{"file_id":"` + file.ID + `","field":"image[]"}]}`)
	_, refs, err := svc.prepareVideoFiles(context.Background(), key, request, GenerationProtocolOpenAI)
	require.NoError(t, err)
	require.Len(t, refs, 2)
	require.NotEqual(t, refs[0].Ref, refs[1].Ref)
	key.ID = 99
	_, _, err = svc.prepareVideoFiles(context.Background(), key, request, GenerationProtocolOpenAI)
	require.ErrorIs(t, err, ErrGenerationFileNotFound)
}

func TestGenerationMultipleFilesActualWorkerAndExactMultipartSignature(t *testing.T) {
	s, account, _ := generationPluginFixture(t, "sora")
	files := &generationMultiFileFixture{}
	s.files = files
	version := s.repo.(*generationPluginFixtureRepo).version
	originalSource := version.Source
	version.Source = strings.Replace(version.Source, `if (hasInputReferenceFile) throw new Error("input_reference must be provided once");`, "", 1)
	version.Source += "\nexport function signRequest(ctx,req){return {headers:{\"X-Multi-Digest\":req.bodySha256}}}"
	svc := &GenerationService{repo: &generationFileTaskRepo{GenerationFileRepository: files}}
	group := int64(3)
	key := &APIKey{UserID: 1, ID: 2, GroupID: &group}
	kind, body := multiFileRequest(t, []string{"input_reference", "input_reference"}, []string{strings.Repeat("a", 600000), strings.Repeat("b", 600000)})
	ctx, normalized, cleanup, err := svc.ReadVideoMultipart(context.Background(), key, kind, bytes.NewReader(body))
	require.NoError(t, err)
	defer cleanup()
	_, refs, err := svc.prepareVideoFiles(ctx, key, normalized, GenerationProtocolOpenAI)
	require.NoError(t, err)
	ctx = context.WithValue(ctx, generationDecodeFilesKey{}, refs)
	customSource := version.Source
	version.Source = originalSource
	_, _, err = s.DecodeVideo(ctx, account, 1, "sora-2", normalized)
	require.ErrorContains(t, err, "input_reference must be provided once", "host support does not expand upstream model capabilities")
	version.Source = customSource
	decoded, action, err := s.DecodeVideo(ctx, account, 1, "sora-2", normalized)
	require.NoError(t, err)
	task := &GenerationTask{ID: "video_" + strings.Repeat("1", 40), UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: account.ID, PluginVersionID: 1, Model: "sora-2", UpstreamModel: "sora-2", Request: decoded, Action: action, Files: refs}
	upstream := &generationFileHTTP{}
	s.gateway.httpUpstream = upstream
	require.NoError(t, s.SubmitTask(ctx, account, task))
	digest := sha256.Sum256(upstream.body)
	require.Equal(t, hex.EncodeToString(digest[:]), upstream.request.Header.Get("X-Multi-Digest"))
	_, params, err := mime.ParseMediaType(upstream.request.Header.Get("Content-Type"))
	require.NoError(t, err)
	reader := multipart.NewReader(bytes.NewReader(upstream.body), params["boundary"])
	count := 0
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if part.FileName() != "" {
			content, err := io.ReadAll(part)
			require.NoError(t, err)
			require.Equal(t, files.data[refs[count].ID], content)
			count++
		}
	}
	require.Equal(t, 2, count)
	require.Less(t, len(decoded), pluginTask.MaxSnapshotBytes)
}
