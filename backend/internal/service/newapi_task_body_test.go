package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

const generationOrderedSource = `
export const meta={apiVersion:1,key:"ordered-video",name:"Ordered video",version:"1.0.0",author:{name:"Test"},models:["sora-2"],protocols:["openai_video"],fetchMode:"per_task",CAPABILITIES};
export const protocols={openai_video:{decodeRequest(ctx){return {kind:"submit",model:ctx.model,action:"text_to_video",requestBody:ctx.body.value};},render(ctx,t){return {id:t.task_id};}}};
function descriptor(ctx,path){if(path!=="/submit" && (ctx.state.cursor!=="state-object" || ctx.taskData.key!=="data-object" || ctx.data.key!=="data-object"))throw Error("state was not a JSON object");return {url:ctx.baseUrl+path,method:"POST",body:ctx.requestBody};}
export function buildSubmitRequest(ctx){return descriptor(ctx,"/submit");}
export function parseSubmitResponse(ctx,res){return {taskId:res.body.id,state:{cursor:"state-object"},taskData:{key:"data-object"}};}
export function buildQueryRequest(ctx){return descriptor(ctx,"/query");}
export function parseTaskResult(ctx,res){return {status:"IN_PROGRESS",state:ctx.state};}
export function buildContentRequest(ctx){return {...descriptor(ctx,"/content"),contentPath:["bytes"],mimeType:"video/mp4"};}
export function listArtifacts(){return [{key:"video",type:"video"}];}
export function signRequest(ctx,req){return {headers:{"X-Body-Digest":req.bodySha256}};}
`

func generationOrderedFixture(t *testing.T, ordered bool) (*NewAPIPluginService, *Account, *generationFixtureHTTP) {
	t.Helper()
	s, account, upstream := generationPluginFixture(t, "sora")
	capabilities := ""
	if ordered {
		capabilities = `requiredCapabilities:["json-order@1"]`
	}
	version := s.repo.(*generationPluginFixtureRepo).version
	version.Source = strings.Replace(generationOrderedSource, "CAPABILITIES", capabilities, 1)
	loaded, err := jsplugin.CompilePlugin(version.Source, jsplugin.Options{Concurrency: 1, HostCapability: runner.SupportsCapability})
	require.NoError(t, err)
	version.Meta, version.Capabilities = loaded.Meta, runner.Describe(loaded)
	return s, account, upstream
}

func TestGenerationOrderedTaskWireAndSignatures(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "ordered"}[ordered], func(t *testing.T) {
			s, account, upstream := generationOrderedFixture(t, ordered)
			ctx := context.Background()
			input := json.RawMessage(`{"z":{"y":1,"b":2},"model":"sora-2","a":"<>&"}`)
			decoded, action, err := s.DecodeVideo(ctx, account, 1, "sora-2", input)
			require.NoError(t, err)
			want := `{"a":"\u003c\u003e\u0026","model":"sora-2","z":{"b":2,"y":1}}`
			if ordered {
				want = string(input)
			}
			require.Equal(t, want, string(decoded))
			task := &GenerationTask{ID: "video_" + strings.Repeat("a", 40), PluginVersionID: 1, Model: "sora-2", UpstreamModel: "sora-2", Action: action, Request: decoded, State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`)}
			check := func(path string) {
				t.Helper()
				request := upstream.requests[len(upstream.requests)-1]
				require.Equal(t, path, request.URL.Path)
				body, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				require.Equal(t, want, string(body))
				digest := sha256.Sum256(body)
				require.Equal(t, hex.EncodeToString(digest[:]), request.Header.Get("X-Body-Digest"))
			}
			upstream.response = `{"id":"ordered-upstream"}`
			require.NoError(t, s.SubmitTask(ctx, account, task))
			check("/submit")
			upstream.response = `{"key":"data-object"}`
			require.NoError(t, s.PollTask(ctx, account, task))
			check("/query")
			upstream.response = `{"done":true,"bytes":"YWJj"}`
			response, err := s.OpenTaskContent(ctx, account, task, http.MethodHead, "video", nil)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			check("/content")
		})
	}
}

func TestGenerationTaskDescriptorRawStringBodyIsNotJSONQuoted(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		s, account, upstream := generationOrderedFixture(t, ordered)
		version := s.repo.(*generationPluginFixtureRepo).version
		version.Source = strings.Replace(version.Source, "body:ctx.requestBody", `body:"raw <>&\\nmessage"`, 1)
		task := &GenerationTask{ID: "video_" + strings.Repeat("b", 40), PluginVersionID: 1, Model: "sora-2", UpstreamModel: "sora-2", Action: "text_to_video", Request: json.RawMessage(`{}`), State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`)}
		for _, path := range []string{"/submit", "/query", "/content"} {
			upstream.response = `{"id":"raw-upstream","key":"data-object","done":true,"bytes":"YWJj"}`
			switch path {
			case "/submit":
				require.NoError(t, s.SubmitTask(context.Background(), account, task))
			case "/query":
				require.NoError(t, s.PollTask(context.Background(), account, task))
			case "/content":
				resp, err := s.OpenTaskContent(context.Background(), account, task, http.MethodGet, "video", nil)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
			}
			request := upstream.requests[len(upstream.requests)-1]
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			require.Equal(t, `raw <>&\nmessage`, string(body))
			digest := sha256.Sum256(body)
			require.Equal(t, hex.EncodeToString(digest[:]), request.Header.Get("X-Body-Digest"))
		}
	}
}

func TestGenerationEmbeddedContentKeepsVendorPlaceholderJSON(t *testing.T) {
	s, account, upstream := generationPluginFixture(t, "sora")
	version := s.repo.(*generationPluginFixtureRepo).version
	version.Source = strings.Replace(version.Source, "export function buildContentRequest(ctx)", "function originalContentRequest(ctx)", 1) + `
export function buildContentRequest(ctx){return {url:ctx.baseUrl+"/content",method:"POST",body:{z:1,a:{__fileRef:"vendor-opaque-value",encoding:"base64"}},contentPath:["bytes"],mimeType:"video/mp4"};}
export function signRequest(ctx,req){return {headers:{"X-Body-Digest":req.bodySha256}};}`
	upstream.response = `{"done":true,"bytes":"YWJj"}`
	task := &GenerationTask{ID: "video_" + strings.Repeat("c", 40), PluginVersionID: 1, Model: "sora-2", UpstreamModel: "sora-2", UpstreamID: "vendor-upstream", Request: json.RawMessage(`{}`)}
	response, err := s.OpenTaskContent(context.Background(), account, task, http.MethodGet, "video", nil)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	request := upstream.requests[len(upstream.requests)-1]
	body, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"z":1,"a":{"__fileRef":"vendor-opaque-value","encoding":"base64"}}`, string(body))
	digest := sha256.Sum256(body)
	require.Equal(t, hex.EncodeToString(digest[:]), request.Header.Get("X-Body-Digest"))
}
