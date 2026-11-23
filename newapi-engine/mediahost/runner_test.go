package mediahost_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/mediahost"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

func TestRunnerMediaHostMetadataTextAndStreamStayScoped(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	source := `export const meta={apiVersion:1,key:"media-rpc",name:"Media",version:"1.0.0",author:{name:"Fixture"},allowedHosts:["example.com"]};
export function ingress(){
 const a=utils.attachments();
 if(a.length===0){if(utils.media("[media:0]")!==null || utils.mediaText("[media:0]")!==null)throw Error("leaked");return {action:"continue"};}
 if(a[0].ref!=="[media:0]" || a[0].size!==6)throw Error("attachments");
 const i=utils.media(a[0].ref),t=utils.mediaText(a[0].ref,{maxChars:3});
 if(i.mime!=="text/plain" || t.text!=="A😀" || !t.truncated)throw Error("text");
 const r=utils.fetch({url:"https://example.com/check",method:"POST",body:JSON.stringify({file:a[0].ref})});
 if(r.status!==200)throw Error("fetch");return {action:"continue"};}
`
	client, err := runner.Start(context.Background(), executable, []string{"-test.run=^TestRunnerWorkerHelper$", "--", "plugin-test-worker"}, source)
	require.NoError(t, err)
	defer client.Close()
	media, err := mediahost.New([]byte(`{"file":"data:text/plain;base64,QfCfmIBC"}`))
	require.NoError(t, err)
	call := jsplugin.TransformContext{Stage: "ingress", Format: "openai", Media: media, Config: json.RawMessage(`{}`)}
	encoded, err := json.Marshal(call)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "QfCfmIBC")
	fetches := 0
	fetcher := func(ctx context.Context, request jsplugin.FetchRequest) (jsplugin.FetchResponse, error) {
		fetches++
		require.NotNil(t, request.BodyStream)
		raw, err := io.ReadAll(request.BodyStream)
		require.NoError(t, err)
		require.EqualValues(t, len(raw), request.BodySize)
		require.JSONEq(t, `{"file":"data:text/plain;base64,QfCfmIBC"}`, string(raw))
		return jsplugin.FetchResponse{Status: 200, Body: []byte(`{}`)}, nil
	}
	_, err = client.Typed(context.Background(), fetcher, "CallTransform", call, jsplugin.TransformRequest{Body: media.View(), Media: media.Projection()})
	require.NoError(t, err)
	require.Equal(t, 1, fetches)
	call.Media = nil
	_, err = client.Typed(context.Background(), nil, "CallTransform", call, jsplugin.TransformRequest{Body: json.RawMessage(`{}`)})
	require.NoError(t, err)
}

func TestRunnerMediaCallbackFloodFailsClosed(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	source := `export const meta={apiVersion:1,key:"media-flood",name:"Media",version:"1.0.0",author:{name:"Fixture"}};export function ingress(){for(let i=0;i<130;i++)utils.media("[media:0]");return {action:"continue"}}`
	client, err := runner.Start(context.Background(), executable, []string{"-test.run=^TestRunnerWorkerHelper$", "--", "plugin-test-worker"}, source)
	require.NoError(t, err)
	defer client.Close()
	media, err := mediahost.New([]byte(`{"a":"data:text/plain;base64,YQ=="}`))
	require.NoError(t, err)
	_, err = client.Typed(context.Background(), nil, "CallTransform", jsplugin.TransformContext{Stage: "ingress", Media: media, Config: json.RawMessage(`{}`)}, jsplugin.TransformRequest{Body: media.View()})
	require.Error(t, err)
	require.True(t, client.IsClosed())
}

func TestRunnerReadsDocumentThroughParentMediaHost(t *testing.T) {
	var document bytes.Buffer
	archive := zip.NewWriter(&document)
	file, err := archive.Create("word/document.xml")
	require.NoError(t, err)
	_, err = io.WriteString(file, `<w:document xmlns:w="urn:word"><w:body><w:p><w:r><w:t>机密内容😀</w:t></w:r></w:p></w:body></w:document>`)
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	executable, err := os.Executable()
	require.NoError(t, err)
	script := `export const meta={apiVersion:1,key:"document-rpc",name:"Document",version:"1.0.0",author:{name:"Fixture"}};
export function ingress(ctx,req){
 const a=utils.attachments();if(a.length!==1 || a[0].rewritable || req.body.file.file_data!==a[0].ref)throw Error("projection");
 const t=utils.mediaText(a[0].ref);if(t.text!=="机密内容😀" || t.truncated)throw Error("document");
 return {action:"continue"};}`
	client, err := runner.Start(context.Background(), executable, []string{"-test.run=^TestRunnerWorkerHelper$", "--", "plugin-test-worker"}, script)
	require.NoError(t, err)
	defer client.Close()
	body := []byte(`{"file":{"filename":"private.docx","file_data":"data:application/octet-stream;base64,` + base64.StdEncoding.EncodeToString(document.Bytes()) + `"}}`)
	media, err := mediahost.New(body)
	require.NoError(t, err)
	_, err = client.Typed(context.Background(), nil, "CallTransform", jsplugin.TransformContext{Stage: "ingress", Media: media, Config: json.RawMessage(`{}`)}, jsplugin.TransformRequest{Body: media.View(), Media: media.Projection()})
	require.NoError(t, err)
	records, _ := media.Records()
	require.Len(t, records, 1)
	require.Equal(t, "checked", records[0].Status)
	encoded, err := json.Marshal(records)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "机密")
	require.NotContains(t, string(encoded), "private")
}

func TestRunnerWorkerHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "plugin-test-worker" {
		return
	}
	if runner.Serve(context.Background(), os.Stdin, os.Stdout) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
