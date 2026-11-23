package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/stretchr/testify/require"
)

type taskEgressMediaFiles struct {
	GenerationFileRepository
	file GenerationFile
	data []byte
}

func (f *taskEgressMediaFiles) GetGenerationFile(ctx context.Context, id string, user, key, group int64) (*GenerationFile, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if id != f.file.ID || user != 4 || key != 5 || group != 7 {
		return nil, ErrGenerationFileNotFound
	}
	return &f.file, nil
}

func (f *taskEgressMediaFiles) OpenGenerationFile(ctx context.Context, id string, user, key, group int64) (io.ReadCloser, error) {
	if _, err := f.GetGenerationFile(ctx, id, user, key, group); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

const taskMediaEgressSource = `export const meta={apiVersion:1,author:{name:"Fixture"},key:"bound-media",name:"Bound media",version:"1.0.0"};
export function egress(ctx,req){
 const items=utils.attachments();
 if(items.length!==1 || items[0].ref!=="[media:0]" || req.body.file!==items[0].ref || items[0].rewritable)throw Error("invalid projection");
 const m=utils.media(items[0].ref);
 if(m.kind!=="image" || m.mime!=="image/png" || m.width!==1 || m.height!==1 || m.filename!=="reference.png")throw Error("invalid metadata");
 if(utils.mediaText(items[0].ref).unchecked!=="unsupported")throw Error("invented image text");
 return {action:"continue"};
}`

func taskEgressMediaFixture(t *testing.T, source string) (*NewAPIPluginService, *requestPluginFixtureRepo, *Account, *GenerationTask, *generationFileHTTP, *taskEgressMediaFiles) {
	t.Helper()
	s, repo, account, task := taskEgressFixture(t, source)
	var content bytes.Buffer
	require.NoError(t, png.Encode(&content, image.NewNRGBA(image.Rect(0, 0, 1, 1))))
	digest := sha256.Sum256(content.Bytes())
	files := &taskEgressMediaFiles{file: GenerationFile{ID: "vfile_" + strings.Repeat("a", 40), Filename: "reference.png", MimeType: "image/png", Size: int64(content.Len()), SHA256: hex.EncodeToString(digest[:])}, data: content.Bytes()}
	s.files = files
	task.Files = []GenerationFileSnapshot{{ID: files.file.ID, SHA256: files.file.SHA256, File: pluginTask.File{Ref: "request_file:input_reference", Field: "input_reference", Filename: files.file.Filename, MimeType: files.file.MimeType, Size: files.file.Size}}}
	task.Request = json.RawMessage(`{"model":"sora-2","file":{"__fileRef":"request_file:input_reference","encoding":"base64"}}`)
	version := repo.versions[1]
	version.Source = strings.Replace(generationOrderedSource, "CAPABILITIES", `requiredCapabilities:["json-order@1"]`, 1)
	loaded, err := jsplugin.CompilePlugin(version.Source, jsplugin.Options{Concurrency: 1, HostCapability: runner.SupportsCapability})
	require.NoError(t, err)
	version.Meta, version.Capabilities = loaded.Meta, runner.Describe(loaded)
	upstream := &generationFileHTTP{}
	s.gateway.httpUpstream = upstream
	return s, repo, account, task, upstream, files
}

func TestTaskEgressBoundFileUsesRealWorkerAndKeepsWireSignature(t *testing.T) {
	s, repo, account, task, upstream, files := taskEgressMediaFixture(t, taskMediaEgressSource)
	require.NoError(t, s.SubmitTask(context.Background(), account, task))
	require.Equal(t, "upstream-file-task", task.UpstreamID)
	require.JSONEq(t, `{"model":"sora-2","file":"`+base64.StdEncoding.EncodeToString(files.data)+`"}`, string(upstream.body))
	digest := sha256.Sum256(upstream.body)
	require.Equal(t, hex.EncodeToString(digest[:]), upstream.request.Header.Get("X-Body-Digest"))
	require.Len(t, repo.runs, 1)
	require.Equal(t, "continue", repo.runs[0].Records[0].Action)
	require.Len(t, repo.runs[0].Records[0].Attachments, 1)
	audit, err := json.Marshal(repo.runs)
	require.NoError(t, err)
	for _, secret := range []string{files.file.ID, "request_file:", "reference.png", base64.StdEncoding.EncodeToString(files.data), "fixture-key"} {
		require.NotContains(t, string(audit), secret)
	}
}

func TestTaskEgressMultipartBlocksBeforeUpstream(t *testing.T) {
	const policy = `export const meta={apiVersion:1,author:{name:"Fixture"},key:"multipart-check",name:"Multipart check",version:"1.0.0"};
export function egress(ctx,req){
 const items=utils.attachments();
 if(req.body.prompt!=="inspect" || items.length!==1 || items[0].path!=="image" || items[0].rewritable)throw Error("wrong multipart projection");
 const m=utils.media(items[0].ref);
 if(m.mime!=="image/png" || m.width!==1 || m.height!==1)throw Error("missing verified file bytes");
 return {action:"reject",code:"image_rejected"};
}`
	s, repo, account, task, upstream, files := taskEgressMediaFixture(t, policy)
	v := repo.versions[1]
	v.Source = strings.Replace(v.Source,
		`export function buildSubmitRequest(ctx){return descriptor(ctx,"/submit");}`,
		`export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/submit",method:"POST",bodyType:"multipart",parts:[{name:"prompt",value:"inspect"},{name:"image",fileRef:"request_file:input_reference"}]};}`, 1)
	err := s.SubmitTask(context.Background(), account, task)
	var failure *RequestPluginFailure
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.Rejected, "%+v", repo.runs)
	require.Nil(t, upstream.request)
	require.Len(t, repo.runs, 1)
	require.Equal(t, "reject", repo.runs[0].Records[0].Action)
	audit, err := json.Marshal(repo.runs)
	require.NoError(t, err)
	for _, secret := range []string{files.file.ID, files.file.Filename, "request_file:", base64.StdEncoding.EncodeToString(files.data)} {
		require.NotContains(t, string(audit), secret)
	}
}

func TestTaskEgressBoundFileRejectsBeforeUpstreamAndRetainsReadonly(t *testing.T) {
	for _, change := range []string{"foreign ref", "changed metadata", "corrupt bytes", "rejected", "effects"} {
		t.Run(change, func(t *testing.T) {
			source := taskMediaEgressSource
			if change == "rejected" {
				source = strings.Replace(source, `return {action:"continue"}`, `return {action:"reject",code:"denied"}`, 1)
			}
			if change == "effects" {
				source = strings.Replace(source, `return {action:"continue"}`, `return {action:"continue",effects:[{type:"media.reencode",media:0,format:"jpeg"}]}`, 1)
			}
			s, repo, account, task, upstream, files := taskEgressMediaFixture(t, source)
			switch change {
			case "foreign ref":
				task.Request = json.RawMessage(strings.Replace(string(task.Request), "request_file:input_reference", "foreign-ref", 1))
			case "changed metadata":
				files.file.SHA256 = strings.Repeat("f", 64)
			case "corrupt bytes":
				files.data = files.data[:4]
			}
			err := s.SubmitTask(context.Background(), account, task)
			var submission *GenerationSubmissionError
			require.ErrorAs(t, err, &submission)
			require.False(t, submission.Uncertain)
			require.Nil(t, upstream.request)
			require.Len(t, repo.runs, 1)
			require.Contains(t, []string{"error", "reject"}, repo.runs[0].Records[0].Action)
		})
	}
}

func TestTaskEgressBoundFileAuthorizedFetch(t *testing.T) {
	source := strings.Replace(taskMediaEgressSource, `version:"1.0.0"`, `version:"1.0.0",allowedHosts:["policy.example.com"]`, 1)
	source = strings.Replace(source, `return {action:"continue"}`, `const reply=utils.fetch({url:"https://policy.example.com/check",method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({file:items[0].ref})});if(reply.status!==200)throw Error("fetch failed");return {action:"continue"}`, 1)
	s, repo, account, task, upstream, files := taskEgressMediaFixture(t, source)
	// Opt-in is snapshotted at admission, separately from the source's hosts.
	raw, err := s.decryptRequestPluginConfig(task.RequestPluginsEncrypted)
	require.NoError(t, err)
	var snapshot taskRequestPluginSnapshot
	require.NoError(t, json.Unmarshal(raw, &snapshot))
	snapshot.Items[0].NetworkEnabled = true
	raw, err = json.Marshal(snapshot)
	require.NoError(t, err)
	task.RequestPluginsEncrypted, err = s.encryptor.Encrypt(string(raw))
	require.NoError(t, err)
	network := &requestNetworkHTTP{inner: upstream}
	s.gateway.httpUpstream = network
	require.NoError(t, s.SubmitTask(context.Background(), account, task))
	require.EqualValues(t, 1, network.calls.Load())
	require.JSONEq(t, `{"file":"data:image/png;base64,`+base64.StdEncoding.EncodeToString(files.data)+`"}`, network.body)
	require.NotContains(t, network.body, files.file.ID)
	require.NotNil(t, upstream.request)
	require.Len(t, repo.runs, 1)
}

func TestTaskEgressBoundFileCapacityFailsBeforeStorageOrNetwork(t *testing.T) {
	s, repo, _, task, upstream, _ := taskEgressMediaFixture(t, taskMediaEgressSource)
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), pkghttputil.NewBodyMemoryBudget(1))
	defer release()
	err := s.taskRequestPluginEgress(ctx, task, task.Request, false)
	var failure *RequestPluginFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "capacity", failure.Class)
	require.Nil(t, upstream.request)
	require.Len(t, repo.runs, 1)
	require.Equal(t, "capacity", repo.runs[0].Records[0].ErrorClass)
}
