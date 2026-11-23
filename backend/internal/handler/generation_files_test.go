//go:build unit

package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type generationFileHandlerRepo struct {
	nativeAdmissionTaskRepo
	service.GenerationFileRepository
	stored int
	size   int64
	file   *service.GenerationFile
	reads  int
}

func (r *generationFileHandlerRepo) GetGenerationFile(_ context.Context, id string, user, key, group int64) (*service.GenerationFile, error) {
	r.reads++
	if r.file == nil || id != r.file.ID || user != 1 || key != 2 || group != 3 {
		return nil, service.ErrGenerationFileNotFound
	}
	return r.file, nil
}

func (r *generationFileHandlerRepo) StoreGenerationFile(_ context.Context, user, key, group int64, filename, kind string, source io.ReadSeeker) (*service.GenerationFile, error) {
	size, err := io.Copy(io.Discard, source)
	if err != nil {
		return nil, err
	}
	r.stored++
	r.size = size
	return &service.GenerationFile{ID: "vfile_" + strings.Repeat("a", 40), Object: "video.file", Filename: filename, MimeType: kind, Size: size}, nil
}
func (r *generationFileHandlerRepo) CleanupGenerationFiles(context.Context, int) (int64, error) {
	return 0, nil
}

func TestGenerationFileHTTPBoundedRawUpload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &generationFileHandlerRepo{}
	svc := service.NewGenerationService(repo, nil, nil, nil, nil, nil)
	defer svc.Stop()
	h := &OpenAIGatewayHandler{generations: svc}
	group := &service.Group{ID: 3, AllowImageGeneration: true}
	key := &service.APIKey{UserID: 1, ID: 2, GroupID: &group.ID, Group: group}
	for _, tc := range []struct {
		name, kind string
		body       io.Reader
		length     int64
		status     int
	}{
		{"raw image", "image/png", strings.NewReader(strings.Repeat("x", 2<<20)), 2 << 20, 200},
		{"wrong MIME", "text/html", strings.NewReader("private"), 7, 400},
		{"empty", "image/png", strings.NewReader(""), 0, 413},
		{"declared oversize", "video/mp4", strings.NewReader("x"), 33 << 20, 413},
		{"unknown length oversize", "video/mp4", io.LimitReader(generationZeroReader{}, (32<<20)+1), -1, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := repo.stored
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/video-files", tc.body)
			c.Request.ContentLength = tc.length
			c.Request.Header.Set("Content-Type", tc.kind)
			c.Request.Header.Set("X-Filename", "reference.png")
			c.Set(string(middleware.ContextKeyAPIKey), key)
			h.GenerationFileUpload(c)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == 200 {
				require.Equal(t, before+1, repo.stored)
				require.EqualValues(t, 2<<20, repo.size)
				require.Contains(t, w.Body.String(), "video.file")
			} else {
				require.Equal(t, before, repo.stored)
			}
		})
	}
}

type generationZeroReader struct{}

func (generationZeroReader) Read(dst []byte) (int, error) { clear(dst); return len(dst), nil }

func TestGenerationStoredFileIngressChecksOwnershipBeforePolicy(t *testing.T) {
	fileID := "vfile_" + strings.Repeat("a", 40)
	request := `{"model":"sora-2","prompt":"ocean","file_references":[{"file_id":"` + fileID + `","field":"input_reference"}]}`
	for _, scenario := range []struct {
		name, effect string
		user         int64
		replay       bool
		status       int
		reads, runs  int
	}{
		{"owned", `{op:"replace",path:"/prompt",value:"approved"}`, 1, false, 200, 1, 1},
		{"another user", `{op:"replace",path:"/prompt",value:"approved"}`, 99, false, 404, 1, 0},
		{"cannot copy a private reference", `{op:"copy",from:"/file_references/0/file_id",path:"/prompt"}`, 1, false, 503, 1, 1},
		{"accepted before expired file and rules", ``, 1, true, 200, 0, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			repo := &generationFileHandlerRepo{file: &service.GenerationFile{ID: fileID, Filename: "reference.png", MimeType: "image/png", Size: 2 << 20}}
			if scenario.replay {
				repo.file = nil
				var parsed map[string]any
				require.NoError(t, json.Unmarshal([]byte(request), &parsed))
				canonical, err := json.Marshal(parsed)
				require.NoError(t, err)
				repo.task = &service.GenerationTask{ID: "video_original", UserID: 1, APIKeyID: 2, IdempotencyKey: "accepted", Fingerprint: service.HashUsageRequestPayload(canonical)}
			}
			svc := service.NewGenerationService(repo, nil, nil, nil, nil, nil)
			defer svc.Stop()
			source := `export const meta={apiVersion:1,key:"stored-file-policy",name:"Stored file policy",version:"1.0.0",author:{name:"Fixture"}};
export function ingress(ctx,req){
 const files=utils.attachments(), f=files[0];
 if(files.length!==1 || f.path!=="input_reference" || f.size!==2097152 || f.rewritable || req.body.file_references!==undefined || utils.mediaText(f.ref).unchecked!=="reference")throw Error("bad stored file projection");
 return {action:"continue",effects:[{type:"json.patch",ops:[` + scenario.effect + `]}]};
}`
			policyRepo := &requestHTTPRepo{version: &service.NewAPIPluginVersion{ID: 1, Source: source}, formats: []string{"task"}}
			plugins, err := service.NewNewAPIPluginService(policyRepo, requestHTTPEncryptor{})
			require.NoError(t, err)
			defer plugins.Stop()
			policy := NewRequestPluginHandler(plugins, svc)
			group := &service.Group{ID: 3}
			key := &service.APIKey{ID: 2, UserID: scenario.user, GroupID: &group.ID, Group: group}
			router := gin.New()
			router.POST("/v1/videos", func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Next()
			}, policy.Ingress, func(c *gin.Context) {
				data, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.JSONEq(t, strings.Replace(request, "ocean", "approved", 1), string(data))
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(request))
			req.Header.Set("Content-Type", "application/json")
			if scenario.replay {
				req.Header.Set("Idempotency-Key", "accepted")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, scenario.status, w.Code, w.Body.String())
			require.Equal(t, scenario.reads, repo.reads)
			require.Len(t, policyRepo.runs, scenario.runs)
			if scenario.runs != 0 {
				audit, err := json.Marshal(policyRepo.runs)
				require.NoError(t, err)
				require.NotContains(t, string(audit), fileID)
				require.NotContains(t, string(audit), "reference.png")
			}
		})
	}
}

func TestGenerationMultipartHTTPStreamsBeforeBodyReadersAndReplays(t *testing.T) {
	gin.SetMode(gin.TestMode)
	image := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 2<<20)...)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "sora-2"))
	require.NoError(t, writer.WriteField("seconds", "8"))
	require.NoError(t, writer.WriteField("prompt", "ocean"))
	part, err := writer.CreateFormFile("input_reference", "input.png")
	require.NoError(t, err)
	_, err = part.Write(image)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	digest := sha256.Sum256(image)
	original, err := json.Marshal(map[string]any{"model": "sora-2", "seconds": "8", "prompt": "ocean", "input_reference": map[string]any{"sha256": hex.EncodeToString(digest[:]), "bytes": len(image), "mime_type": "image/png"}})
	require.NoError(t, err)
	repo := &generationFileHandlerRepo{nativeAdmissionTaskRepo: nativeAdmissionTaskRepo{task: &service.GenerationTask{ID: "video_original", UserID: 1, APIKeyID: 2, IdempotencyKey: "accepted", Fingerprint: service.HashUsageRequestPayload(original)}}}
	svc := service.NewGenerationService(repo, nil, nil, nil, nil, nil)
	defer svc.Stop()
	h := &OpenAIGatewayHandler{generations: svc} // Replay has no scheduler or live plugin.
	policyRepo := &requestHTTPRepo{version: &service.NewAPIPluginVersion{ID: 1, Source: `export const meta={apiVersion:1,key:"upload-policy",name:"Upload policy",version:"1.0.0",author:{name:"Fixture"}};
export function ingress(ctx, req) {
 const attachments = utils.attachments();
 const file = attachments[0];
 if (attachments.length !== 1 || file.path !== "input_reference" || file.mime !== "image/png" || file.size !== 2097160 || file.rewritable || req.body.input_reference !== undefined) throw Error("bad upload projection");
 if (req.body.prompt === "deny") return {action:"reject",code:"blocked"};
 const op = req.body.prompt === "replace-model" ? {op:"replace",path:"/model",value:"another-model"} : {op:"replace",path:"/prompt",value:"approved"};
 return {action:"continue",effects:[{type:"json.patch",ops:[op]}]};
}`}, models: []string{"sora-2"}}
	policyPlugins, err := service.NewNewAPIPluginService(policyRepo, requestHTTPEncryptor{})
	require.NoError(t, err)
	defer policyPlugins.Stop()
	policy := NewRequestPluginHandler(policyPlugins, svc)
	group := &service.Group{ID: 3, AllowImageGeneration: true}
	key := &service.APIKey{UserID: 1, ID: 2, GroupID: &group.ID, Group: group}
	for _, tc := range []struct {
		name, path, encoding, prompt string
		budget, length               int64
		changed, replay, policy      bool
		status                       int
	}{
		{name: "unknown length SDK upload", path: "/v1/videos", length: -1, budget: 64 << 20, status: 200},
		{name: "accepted root alias", path: "/videos", length: -1, budget: 64 << 20, replay: true, status: 200},
		{name: "accepted upload before new JSON policy", path: "/v1/videos", length: -1, budget: 64 << 20, replay: true, policy: true, status: 200},
		{name: "new upload projects attachment and replaces prompt", path: "/v1/videos", length: -1, budget: 64 << 20, policy: true, status: 200},
		{name: "policy rejects upload before storage", path: "/v1/videos", length: -1, budget: 64 << 20, policy: true, prompt: "deny", status: 403},
		{name: "policy cannot change upload model", path: "/v1/videos", length: -1, budget: 64 << 20, policy: true, prompt: "replace-model", status: 503},
		{name: "same key changed file", path: "/v1/videos", length: -1, budget: 64 << 20, replay: true, changed: true, status: 409},
		{name: "shared memory overload", path: "/v1/videos", length: -1, budget: 1024, status: 503},
		{name: "declared oversized", path: "/v1/videos", length: service.GenerationMultipartMaxBytes + 1, budget: 64 << 20, status: 413},
		{name: "compressed upload rejected", path: "/v1/videos", length: -1, budget: 64 << 20, encoding: "gzip", status: 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beforeRuns := len(policyRepo.runs)
			router := gin.New()
			router.POST(tc.path, middleware.RequestBodyLimit(64<<20, pkghttputil.NewBodyMemoryBudget(tc.budget)), func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Next()
			}, h.GenerationMultipartIngress, func(c *gin.Context) {
				if tc.policy {
					policy.Ingress(c)
					return
				}
				c.Next()
			}, func(c *gin.Context) {
				data, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
				require.NoError(t, err)
				expected := `{"model":"sora-2","seconds":"8","prompt":"ocean"}`
				if tc.policy && !tc.replay {
					expected = `{"model":"sora-2","seconds":"8","prompt":"approved"}`
				}
				require.JSONEq(t, expected, string(data))
				require.True(t, service.HasGenerationMultipartFile(c.Request.Context()))
				if tc.replay {
					require.True(t, h.TryPluginVideo(c, service.PlatformOpenAI))
					return
				}
				c.JSON(200, gin.H{"streamed": true})
			})
			data := append([]byte(nil), body.Bytes()...)
			if tc.prompt != "" {
				data = bytes.Replace(data, []byte("\r\nocean\r\n"), []byte("\r\n"+tc.prompt+"\r\n"), 1)
			}
			if tc.changed {
				index := bytes.Index(data, image)
				require.Positive(t, index)
				data[index+len(image)-1] = 'y'
			}
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(data))
			req.ContentLength = tc.length
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("Content-Encoding", tc.encoding)
			if tc.replay {
				req.Header.Set("Idempotency-Key", "accepted")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == 200 && tc.replay {
				require.Contains(t, w.Body.String(), "video_original")
			}
			if tc.policy && !tc.replay {
				require.Len(t, policyRepo.runs, beforeRuns+1)
				require.Len(t, policyRepo.runs[beforeRuns].Records[0].Attachments, 1)
				require.Equal(t, "image/png", policyRepo.runs[beforeRuns].Records[0].Attachments[0].Mime)
			} else {
				require.Len(t, policyRepo.runs, beforeRuns)
			}
			require.Zero(t, repo.stored, "replay/normalization do not reserve persistent file storage")
		})
	}
}

func TestGenerationMultipartHTTPMultipleFileFieldsAreStreamed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.WriteField("model", "fixture-video"))
	for range 4 {
		part, err := w.CreateFormFile("image[]", "fixture.png")
		require.NoError(t, err)
		_, err = part.Write(append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 600000)...))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	repo := &generationFileHandlerRepo{}
	svc := service.NewGenerationService(repo, nil, nil, nil, nil, nil)
	defer svc.Stop()
	h := &OpenAIGatewayHandler{generations: svc}
	group := &service.Group{ID: 3, AllowImageGeneration: true}
	router := gin.New()
	router.POST("/v1/videos", middleware.RequestBodyLimit(64<<20, pkghttputil.NewBodyMemoryBudget(64<<20)), func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group.ID, Group: group})
	}, h.GenerationMultipartIngress, func(c *gin.Context) {
		data, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		require.NoError(t, err)
		require.JSONEq(t, `{"model":"fixture-video"}`, string(data))
		require.True(t, service.HasGenerationMultipartFile(c.Request.Context()))
		c.JSON(200, gin.H{"streamed": true})
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body.Bytes()))
	req.ContentLength = -1
	req.Header.Set("Content-Type", w.FormDataContentType())
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	require.Zero(t, repo.stored)
}
