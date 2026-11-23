package handler

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The production service launches its executable in worker mode. The handler
// test binary implements that same boundary, so HTTP tests execute real JS.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "plugin" && os.Args[2] == "worker" {
		if runner.Serve(context.Background(), os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type requestHTTPEncryptor struct{}

func (requestHTTPEncryptor) Encrypt(value string) (string, error) { return "fixture:" + value, nil }
func (requestHTTPEncryptor) Decrypt(value string) (string, error) {
	return strings.TrimPrefix(value, "fixture:"), nil
}

type requestHTTPRepo struct {
	service.NewAPIPluginRepository
	service.RequestPluginRepository
	version   *service.NewAPIPluginVersion
	models    []string
	stages    []string
	formats   []string
	scope     string
	runs      []service.RequestPluginRun
	failAudit bool
}

func (r *requestHTTPRepo) ListRequestPluginInstances(context.Context, bool) ([]service.RequestPluginInstance, error) {
	stages := r.stages
	if len(stages) == 0 {
		stages = []string{"ingress"}
	}
	scope := r.scope
	if scope == "" {
		scope = "global"
	}
	var accounts []int64
	if scope == "account" {
		accounts = []int64{17}
	}
	return []service.RequestPluginInstance{{ID: 3, Name: "HTTP policy", PluginVersionID: 1, Version: r.version, Models: r.models, Formats: r.formats, AccountIDs: accounts, Enabled: true, Scope: scope, Stages: stages, ConfigEncrypted: `fixture:{}`}}, nil
}
func (r *requestHTTPRepo) RecordRequestPluginRun(_ context.Context, item service.RequestPluginRun) error {
	if r.failAudit {
		return errors.New("fixture audit outage")
	}
	r.runs = append(r.runs, item)
	return nil
}

func TestRequestPluginIngressHTTPOriginalBodyAndRejection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	source := `export const meta={apiVersion:1,key:"http-filter",name:"HTTP filter",version:"1.0.0",author:{name:"Fixture"}};
export function ingress(ctx,req){if(req.body.prompt==="deny"){return {action:"reject",code:"denied",message:"private rejection hint"};}return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/prompt",value:"sanitized"}]}]};}`
	repo := &requestHTTPRepo{version: &service.NewAPIPluginVersion{ID: 1, Source: source}}
	svc, err := service.NewNewAPIPluginService(repo, requestHTTPEncryptor{})
	require.NoError(t, err)
	defer svc.Stop()
	h := NewRequestPluginHandler(svc, nil)
	r := gin.New()
	r.Use(middleware.ClientRequestID())
	r.Use(func(c *gin.Context) {
		id := int64(7)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &id, Name: "fixture"})
		c.Next()
	})
	r.Use(h.Ingress)
	forwarded := 0
	paths := []string{"/v1/chat/completions", "/v1/embeddings", "/v1/images/generations", "/v1/images/edits", "/v1/responses", "/responses", "/backend-api/codex/responses", "/v1/messages", "/antigravity/v1/messages", "/v1/videos", "/v1beta/models/*modelAction", "/antigravity/v1beta/models/*modelAction"}
	for _, path := range paths {
		r.POST(path, func(c *gin.Context) {
			forwarded++
			raw, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			require.EqualValues(t, len(raw), c.Request.ContentLength)
			require.Equal(t, "model-a", requestmodel.FromJSON(raw))
			c.Data(http.StatusOK, "application/json", raw)
		})
	}
	for _, path := range []string{"/v1/chat/completions", "/v1/embeddings", "/v1/images/generations", "/v1/images/edits", "/v1/responses", "/responses", "/backend-api/codex/responses", "/v1/messages", "/antigravity/v1/messages", "/v1/videos", "/v1beta/models/model-a:generateContent", "/v1beta/models/model-a:streamGenerateContent", "/antigravity/v1beta/models/model-a:generateContent"} {
		for _, prompt := range []string{"allow", "deny"} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"model-a","prompt":"`+prompt+`"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if prompt == "allow" {
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), "sanitized")
			} else {
				require.Equal(t, 403, w.Code, w.Body.String())
				require.NotContains(t, w.Body.String(), "private rejection hint")
			}
		}
	}
	require.Equal(t, 13, forwarded, "rejected requests must never reach a provider or billing handler")
	require.Len(t, repo.runs, 26)
	encoded, _ := json.Marshal(repo.runs)
	require.NotContains(t, string(encoded), "sanitized")
	require.NotContains(t, string(encoded), "private rejection hint")
	// A failed audit write cannot forward a changed request without a record.
	repo.failAudit = true
	req := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"model-a","prompt":"allow"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 503, w.Code)
	require.Equal(t, 13, forwarded)
}

func TestRequestPluginImagesDefaultModelCannotBypassScopedIngress(t *testing.T) {
	source := `export const meta={apiVersion:1,key:"image-filter",name:"Images",version:"1.0.0",author:{name:"Fixture"},scope:{models:["gpt-image-2"]}};export function ingress(ctx){if(ctx.model!=="gpt-image-2")throw Error("wrong image default");return {action:"reject",code:"blocked"};}`
	repo := &requestHTTPRepo{version: &service.NewAPIPluginVersion{ID: 1, Source: source}, models: []string{service.DefaultOpenAIImagesModel}, formats: []string{"openai"}}
	svc, err := service.NewNewAPIPluginService(repo, requestHTTPEncryptor{})
	require.NoError(t, err)
	defer svc.Stop()
	h := NewRequestPluginHandler(svc, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		id := int64(7)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &id})
		c.Next()
	})
	r.Use(h.Ingress)
	for _, path := range []string{"/v1/images/generations", "/v1/images/edits", "/images/generations", "/images/edits"} {
		r.POST(path, func(c *gin.Context) { t.Error("a model-scoped rejection reached the image gateway") })
		for _, body := range []string{`{"prompt":"draw"}`, `{"prompt":"draw","session":{"model":"unrelated"}}`} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			r.ServeHTTP(response, req)
			require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		}
	}
	require.Len(t, repo.runs, 8)
}

func TestRequestPluginIngressHTTPReportOnlyCaptureLifecycle(t *testing.T) {
	source := `export const meta={apiVersion:1,key:"http-report",name:"Report",version:"1.0.0",author:{name:"Fixture"}};export function report(){}`
	repo := &requestHTTPRepo{version: &service.NewAPIPluginVersion{ID: 1, Source: source}, stages: []string{"report"}, formats: []string{"openai"}, scope: "account"}
	plugins, err := service.NewNewAPIPluginService(repo, requestHTTPEncryptor{})
	require.NoError(t, err)
	defer plugins.Stop()
	h := NewRequestPluginHandler(plugins, nil)
	r := gin.New()
	var original gin.ResponseWriter
	restored := false
	r.Use(func(c *gin.Context) {
		original = c.Writer
		id := int64(7)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &id, User: &service.User{Username: "fixture"}})
		c.Next()
		restored = c.Writer == original
	})
	r.Use(h.Ingress)
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		_, ok := c.Writer.(*service.RequestPluginHTTPReport)
		require.True(t, ok, "report-only plans must capture without requiring ingress")
		body, readErr := io.ReadAll(c.Request.Body)
		require.NoError(t, readErr)
		require.JSONEq(t, `{"model":"text","messages":[]}`, string(body))
		c.JSON(403, gin.H{"error": gin.H{"message": "No eligible account"}})
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"text","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, 403, w.Code)
	require.True(t, restored)
	require.Empty(t, repo.runs, "report-only is not an empty ingress audit")
	require.Zero(t, plugins.TaskReportStats().Accepted, "no selected Provider means no account report")
}

func TestRequestPluginVideoReplayPreservesOriginalClientIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	source := `export const meta={apiVersion:1,key:"video-reject",name:"Video reject",version:"1.0.0",author:{name:"Fixture"}};
export function ingress(){return {action:"reject",code:"denied",message:"new rule must not reject an accepted job"};}`
	repo := &requestHTTPRepo{version: &service.NewAPIPluginVersion{ID: 1, Source: source}, models: []string{"sora-2"}}
	plugins, err := service.NewNewAPIPluginService(repo, requestHTTPEncryptor{})
	require.NoError(t, err)
	defer plugins.Stop()
	task := &service.GenerationTask{ID: "video_original", UserID: 1, APIKeyID: 2, IdempotencyKey: "accepted", Fingerprint: service.HashUsageRequestPayload([]byte(`{"model":"sora-2","prompt":"Ocean"}`))}
	generations := service.NewGenerationService(&generationGatewayReplayFixture{task: task}, nil, nil, nil, nil, nil)
	defer generations.Stop()
	h := NewRequestPluginHandler(plugins, generations)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		group := int64(7)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group})
		c.Next()
	})
	r.Use(h.Ingress)
	forwarded := 0
	r.POST("/v1/videos", func(c *gin.Context) { forwarded++; c.Status(http.StatusNoContent) })
	for _, tc := range []struct {
		body, key string
		status    int
	}{
		{`{"prompt":"Ocean", "model":"sora-2"}`, "accepted", http.StatusOK},
		{`{"model":"sora-2","prompt":"Changed"}`, "accepted", http.StatusConflict},
		{`{"model":"sora-2","prompt":"Ocean"}`, "new-key", http.StatusForbidden},
		{`{"model":"sora-2","prompt":"` + strings.Repeat("x", service.MaxRequestPluginBody) + `"}`, "accepted", http.StatusRequestEntityTooLarge},
		// Another model's policy must not impose JSON/video normalization or
		// the processing size limit on a request outside its exact model scope.
		{`{"model":"other","prompt":"` + strings.Repeat("x", service.MaxRequestPluginBody) + `"}`, "accepted", http.StatusNoContent},
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", tc.key)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, tc.status, w.Code, w.Body.String())
		if tc.status == http.StatusOK {
			require.Contains(t, w.Body.String(), task.ID)
			require.Equal(t, "/v1/videos/"+task.ID, w.Header().Get("Location"))
			require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
		}
	}
	require.Equal(t, 1, forwarded, "only the out-of-scope request reaches the next handler")
	require.Len(t, repo.runs, 1, "replays and conflicts do not rerun policy or create execution records")
	require.Equal(t, "reject", repo.runs[0].Records[0].Action)
}

func TestTaskEgressHTTPDoesNotExecuteIngressOrAffectTextEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	source := `export const meta={apiVersion:1,key:"task-egress",name:"Task egress",version:"1.0.0",author:{name:"Fixture"}};export function ingress(){throw new Error("must not run")};export function egress(){return {action:"continue"}};`
	repo := &requestHTTPRepo{version: &service.NewAPIPluginVersion{ID: 1, Source: source}, stages: []string{"egress"}, formats: []string{"task"}}
	plugins, err := service.NewNewAPIPluginService(repo, requestHTTPEncryptor{})
	require.NoError(t, err)
	defer plugins.Stop()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		group := int64(7)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group})
		c.Next()
	}, NewRequestPluginHandler(plugins, nil).Ingress)
	for _, path := range []string{"/v1/videos", "/v1/chat/completions"} {
		r.POST(path, func(c *gin.Context) {
			body, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			c.Data(200, "application/json", body)
		})
	}
	body := `{"model":"same-model","prompt":"private prompt"}`
	for _, path := range []string{"/v1/videos", "/v1/chat/completions"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.JSONEq(t, body, w.Body.String())
	}
	require.Empty(t, repo.runs, "egress runs after the task hold in its worker, never in ingress middleware")
}

func TestProviderAccountIngressHTTPDefersBodyRestrictionsAndExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	source := `export const meta={apiVersion:1,key:"account-ingress",name:"Account ingress",version:"1.0.0",author:{name:"Fixture"}};export function ingress(){throw new Error("account not selected yet")}`
	repo := &requestHTTPRepo{version: &service.NewAPIPluginVersion{ID: 1, Source: source}, scope: "account", stages: []string{"ingress"}, formats: []string{"openai"}}
	plugins, err := service.NewNewAPIPluginService(repo, requestHTTPEncryptor{})
	require.NoError(t, err)
	defer plugins.Stop()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		group := int64(7)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group})
		c.Next()
	}, NewRequestPluginHandler(plugins, nil).Ingress)
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.Data(http.StatusOK, "application/octet-stream", body)
	})
	for _, contentType := range []string{"application/json", "application/octet-stream"} {
		body := `{"model":"same-model","messages":[]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Equal(t, body, w.Body.String())
	}
	require.Empty(t, repo.runs, "an account rule does not run or impose early ingress restrictions")
}

func handlerTestPNGBytes(t *testing.T, minSize int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 12, G: 180, B: 90, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	raw := buf.Bytes()
	if minSize <= len(raw) {
		return raw
	}
	const iendLen = 12
	padLen := minSize - len(raw) + 64
	chunkData := append([]byte("Note\x00"), bytes.Repeat([]byte("Z"), padLen)...)
	var out bytes.Buffer
	out.Write(raw[:len(raw)-iendLen])
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(chunkData)))
	out.Write(lenBuf[:])
	out.WriteString("tEXt")
	out.Write(chunkData)
	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte("tEXt"))
	_, _ = crc.Write(chunkData)
	binary.BigEndian.PutUint32(lenBuf[:], crc.Sum32())
	out.Write(lenBuf[:])
	out.Write(raw[len(raw)-iendLen:])
	return out.Bytes()
}

func buildHandlerMultipartEdit(t *testing.T, boundary, model, prompt string, pngBytes []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	require.NoError(t, writer.SetBoundary(boundary))
	require.NoError(t, writer.WriteField("model", model))
	require.NoError(t, writer.WriteField("prompt", prompt))
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", `form-data; name="image"; filename="edit.png"`)
	hdr.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(hdr)
	require.NoError(t, err)
	_, err = part.Write(pngBytes)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buf.Bytes(), writer.FormDataContentType()
}

func TestRequestPluginHandlerImagesEditsMultipartIngress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	smallPNG := handlerTestPNGBytes(t, 0)
	largePNG := handlerTestPNGBytes(t, service.MaxRequestPluginBody+64*1024)
	source := `export const meta={apiVersion:1,key:"mp-img-ingress",name:"Multipart images ingress",version:"1.0.0",author:{name:"Fixture"}};
export function ingress(ctx,req){
 if(req.body.prompt==="deny")return {action:"reject",code:"denied",message:"private deny"};
 if(req.body.prompt==="bad-model")return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/model",value:"hijacked"}]}]};
 if(req.body.prompt==="boundary-inject")return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/prompt",value:"x\r\n--handler-mp-boundary\r\n"}]}]};
 const items=utils.attachments();
 if(items.length!==1)throw new Error("missing multipart media");
 const info=utils.media(items[0].ref);
 if(info.mime!=="image/png" || info.width!==2 || info.height!==2)throw new Error("bad media info");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/prompt",value:"sanitized"}]}]};
}`
	repo := &requestHTTPRepo{version: &service.NewAPIPluginVersion{ID: 1, Source: source}, stages: []string{"ingress"}, formats: []string{"openai"}}
	plugins, err := service.NewNewAPIPluginService(repo, requestHTTPEncryptor{})
	require.NoError(t, err)
	defer plugins.Stop()

	r := gin.New()
	r.Use(middleware.ClientRequestID())
	r.Use(func(c *gin.Context) {
		group := int64(7)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group})
		c.Next()
	}, NewRequestPluginHandler(plugins, nil).Ingress)

	forwarded := 0
	var lastSent []byte
	var lastCT string
	r.POST("/v1/images/edits", func(c *gin.Context) {
		forwarded++
		lastCT = c.GetHeader("Content-Type")
		raw, readErr := io.ReadAll(c.Request.Body)
		require.NoError(t, readErr)
		lastSent = raw
		c.Status(http.StatusOK)
	})

	for _, tc := range []struct {
		name   string
		prompt string
		png    []byte
		status int
	}{
		{name: "small-allow", prompt: "allow", png: smallPNG, status: http.StatusOK},
		{name: "oversized-allow", prompt: "allow", png: largePNG, status: http.StatusOK},
		{name: "reject", prompt: "deny", png: smallPNG, status: http.StatusForbidden},
		{name: "mutate-model-blocked", prompt: "bad-model", png: smallPNG, status: http.StatusServiceUnavailable},
		{name: "boundary-injection-blocked", prompt: "boundary-inject", png: smallPNG, status: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rawBody, ct := buildHandlerMultipartEdit(t, "handler-mp-boundary", "gpt-image-2", tc.prompt, tc.png)
			req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(rawBody))
			req.Header.Set("Content-Type", ct)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == http.StatusForbidden {
				require.NotContains(t, w.Body.String(), "private deny")
			}
			if tc.status == http.StatusOK {
				_, params, parseErr := mime.ParseMediaType(lastCT)
				require.NoError(t, parseErr)
				mr := multipart.NewReader(bytes.NewReader(lastSent), params["boundary"])
				gotFields := map[string]string{}
				var gotImage []byte
				for {
					part, partErr := mr.NextRawPart()
					if errors.Is(partErr, io.EOF) {
						break
					}
					require.NoError(t, partErr)
					data, readErr := io.ReadAll(part)
					_ = part.Close()
					require.NoError(t, readErr)
					if part.FileName() != "" {
						gotImage = data
					} else {
						gotFields[part.FormName()] = string(data)
					}
				}
				require.Equal(t, "gpt-image-2", gotFields["model"])
				require.Equal(t, "sanitized", gotFields["prompt"])
				require.Equal(t, tc.png, gotImage)
			}
		})
	}
	require.Equal(t, 2, forwarded)
}

func TestRequestPluginHandlerReportOnlyRoutesSkipEarlyIngress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	source := `export const meta={apiVersion:1,key:"report-only-routes",name:"Report only",version:"1.0.0",author:{name:"Fixture"}};
export function ingress(){throw new Error("report-only routes must not run early ingress");}`
	repo := &requestHTTPRepo{version: &service.NewAPIPluginVersion{ID: 1, Source: source}, stages: []string{"ingress"}, formats: []string{"openai"}}
	plugins, err := service.NewNewAPIPluginService(repo, requestHTTPEncryptor{})
	require.NoError(t, err)
	defer plugins.Stop()

	r := gin.New()
	r.Use(middleware.ClientRequestID())
	r.Use(func(c *gin.Context) {
		group := int64(7)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group})
		c.Next()
	}, NewRequestPluginHandler(plugins, nil).Ingress)

	forwarded := 0
	for _, path := range []string{"/v1/tts", "/v1/stt", "/v1/web_search", "/v1/x_search", "/v1/alpha/search"} {
		p := path
		r.POST(p, func(c *gin.Context) {
			forwarded++
			raw, readErr := io.ReadAll(c.Request.Body)
			require.NoError(t, readErr)
			if p == "/v1/tts" {
				c.Data(http.StatusOK, "audio/mpeg", []byte("\xff\xfb\x90\x00AUDIO"))
				return
			}
			c.Data(http.StatusOK, "application/json", raw)
		})
	}

	var sttBuf bytes.Buffer
	sttWriter := multipart.NewWriter(&sttBuf)
	require.NoError(t, sttWriter.WriteField("language", "en"))
	sttPart, err := sttWriter.CreateFormFile("file", "audio.wav")
	require.NoError(t, err)
	_, err = sttPart.Write([]byte("RIFF\x24\x00\x00\x00WAVE"))
	require.NoError(t, err)
	require.NoError(t, sttWriter.Close())

	cases := []struct {
		path string
		ct   string
		body []byte
	}{
		{path: "/v1/tts", ct: "application/json", body: []byte(`{"input":"hello"}`)},
		{path: "/v1/stt", ct: sttWriter.FormDataContentType(), body: sttBuf.Bytes()},
		{path: "/v1/web_search", ct: "application/json", body: []byte(`{"query":"news"}`)},
		{path: "/v1/x_search", ct: "application/json", body: []byte(`{"query":"posts"}`)},
		{path: "/v1/alpha/search", ct: "application/json", body: []byte(`{"model":"gpt-5.6-sol","commands":{"search_query":[{"q":"go"}]}}`)},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(tc.body))
		req.Header.Set("Content-Type", tc.ct)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, tc.path+": "+w.Body.String())
	}
	require.Equal(t, len(cases), forwarded)
	require.Empty(t, repo.runs, "report-only routes must bypass early ingress rules")
}
