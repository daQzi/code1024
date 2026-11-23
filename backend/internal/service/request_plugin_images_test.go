//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func nativeImagesPolicyFixture(t *testing.T, path, source string, stages []string) (*NewAPIPluginService, *Account, *generationFixtureHTTP, *RequestPluginPlan) {
	t.Helper()
	s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, source, stages, path)
	nativeRequestAccount(account, false)
	account.Credentials["model_mapping"] = map[string]any{"gpt-image-2": "gpt-image-2-vendor"}
	plan.call.Model = "gpt-image-2"
	return s, account, upstream, plan
}

func TestNativeImagesPolicyResolvesDefaultAndChannelBeforeAccount(t *testing.T) {
	account := &Account{Credentials: map[string]any{"model_mapping": map[string]any{
		"gpt-image-2": "gpt-image-2-default-upstream", "gpt-image-2.5": "gpt-image-2.5-upstream",
	}}}
	for _, tc := range []struct{ body, channel, expected string }{
		{`{"prompt":"draw"}`, "", "gpt-image-2-default-upstream"},
		{`{"model":"gpt-image-2"}`, "", "gpt-image-2-default-upstream"},
		{`{"model":"gpt-image-2"}`, "gpt-image-2.5", "gpt-image-2.5-upstream"},
	} {
		require.Equal(t, tc.expected, nativeRequestPluginUpstreamModel(account, []byte(tc.body), openAIImagesGenerationsEndpoint, tc.channel))
	}
}

func TestNativeImagesPolicyUsesActualResolvedModel(t *testing.T) {
	for _, tc := range []struct {
		name, client, composite, channel, resolved string
	}{
		{"default", "", "", "", "gpt-image-2"},
		{"explicit", "gpt-image-2", "", "", "gpt-image-2"},
		{"composite", "public-image", "gpt-image-2.5", "", "gpt-image-2.5"},
		{"channel", "gpt-image-2", "", "gpt-image-2.5", "gpt-image-2.5"},
		{"channel overrides composite", "public-image", "gpt-image-2", "gpt-image-2.5", "gpt-image-2.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := tc.resolved + "-vendor"
			source := providerPolicyMeta + fmt.Sprintf(`
export function request(ctx){if(ctx.upstreamModel!==%q)throw Error("wrong resolved request model");return {action:"continue"};}
export function outbound(ctx,req){if(ctx.upstreamModel!==%q || req.body.model!==%q)throw Error("wrong final model");return {action:"continue"};}`, expected, expected, expected)
			s, account, upstream, plan := nativeImagesPolicyFixture(t, openAIImagesGenerationsEndpoint, source, []string{"request", "outbound"})
			account.Credentials["model_mapping"] = map[string]any{tc.resolved: expected}
			body := []byte(`{"prompt":"draw"}`)
			if tc.client != "" {
				body = []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw"}`, tc.client))
				plan.call.Model = tc.client
			}
			c, _ := providerContext(openAIImagesGenerationsEndpoint, string(body))
			c.Request.Header.Set("Content-Type", "application/json")
			if tc.composite != "" {
				c.Request = c.Request.WithContext(WithCompositeRouteDecision(c.Request.Context(), CompositeRouteDecision{Matched: true, TargetPlatform: PlatformOpenAI, PublicModel: tc.client, UpstreamModel: tc.composite}))
			}
			parsed, err := s.gateway.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			upstream.response = `{"data":[{"b64_json":"ZmluYWw="}]}`
			result, err := s.gateway.ForwardImages(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, parsed, tc.channel)
			require.NoError(t, err)
			require.Equal(t, expected, result.UpstreamModel)
			require.Len(t, upstream.requests, 1)
			sent, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.Equal(t, expected, gjson.GetBytes(sent, "model").String())
		})
	}
}

func TestNativeImagesRequestStagesAndConfirmedReport(t *testing.T) {
	for _, path := range []string{openAIImagesGenerationsEndpoint, openAIImagesEditsEndpoint} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", path, stream), func(t *testing.T) {
				source := globalHTTPReportSource + `
export function request(ctx,req){
 if(ctx.format!=="openai" || !ctx.path.includes("/images/") || ctx.model!=="gpt-image-2" || ctx.upstreamModel!=="gpt-image-2-vendor" || req.body.prompt!=="draw" || ctx.channel.type!==1)throw Error("wrong request");
 return {action:"continue"};
}
export function ingress(ctx,req){
 if(req.body.model!=="gpt-image-2-vendor" || req.body.prompt!=="draw")throw Error("wrong late ingress");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/prompt",value:"sanitized"}]}]};
}
export function outbound(ctx,req){
 if(req.body.prompt!=="sanitized" || req.headers?.Authorization)throw Error("wrong outbound");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/user",value:"policy"}]},{type:"header.patch",set:{"X-Image-Policy":"checked"}}]};
}
export function egress(ctx,req){
 if(req.body.user!=="policy" || req.headers?.["X-Image-Policy"]!=="checked" || req.body.reasoning_effort || req.body.stream_options)throw Error("text generation policy leaked into images");
 return {action:"continue"};
}
export function reply(){throw Error("images are outside the text reply contract");}
`
				s, account, upstream, plan := nativeImagesPolicyFixture(t, path, source, []string{"ingress", "request", "outbound", "egress", "reply", "report"})
				body := []byte(fmt.Sprintf(`{"model":"gpt-image-2","prompt":"draw","n":1,"size":"1024x1024","stream":%t,"response_format":"b64_json"}`, stream))
				if path == openAIImagesEditsEndpoint {
					body = []byte(strings.TrimSuffix(string(body), "}") + `,"images":[{"image_url":"https://source.example/input.png"}]}`)
				}
				upstream.response = `{"created":1710000007,"data":[{"b64_json":"ZmluYWw="}],"usage":{"input_tokens":10,"output_tokens":18,"output_tokens_details":{"image_tokens":8}}}`
				if stream {
					upstream.contentType = "text/event-stream"
					upstream.response = "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"ZmluYWw=\",\"usage\":{\"input_tokens\":10,\"output_tokens\":18,\"output_tokens_details\":{\"image_tokens\":8}}}\n\ndata: [DONE]\n\n"
				}
				reports := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 4)}
				s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: reports}
				s.taskReports = newTaskReportExecutor(s)
				t.Cleanup(s.taskReports.close)
				c, response := providerContext(path, string(body))
				c.Request.Header.Set("Content-Type", "application/json")
				parsed, err := s.gateway.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				original := c.Request
				ctx := WithRequestPluginPlan(c.Request.Context(), plan)
				report := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
				require.NotNil(t, report)
				c.Writer = report
				result, err := s.gateway.ForwardImages(ctx, c, account, body, parsed, "")
				require.NoError(t, err, "runs: %+v", s.repo.(*requestPluginFixtureRepo).runs)
				require.Equal(t, http.StatusOK, response.Code)
				require.Same(t, original, c.Request)
				require.Len(t, upstream.requests, 1)
				sent, err := io.ReadAll(upstream.requests[0].Body)
				require.NoError(t, err)
				require.Equal(t, path, upstream.requests[0].URL.Path)
				require.Equal(t, "gpt-image-2-vendor", gjson.GetBytes(sent, "model").Str)
				require.Equal(t, "sanitized", gjson.GetBytes(sent, "prompt").Str)
				require.Equal(t, "policy", gjson.GetBytes(sent, "user").Str)
				require.Equal(t, "checked", upstream.requests[0].Header.Get("X-Image-Policy"))
				require.Equal(t, "Bearer upstream-fixture-key", upstream.requests[0].Header.Get("Authorization"))
				require.Equal(t, 1, result.ImageCount)
				require.Contains(t, response.Body.String(), "ZmluYWw=")
				report.Finish(body)
				require.Empty(t, reports.requests, "HTTP completion alone must not report an estimated or zero charge")
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
				billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
				price, groupID := 0.2, int64(7)
				result.RequestID = "image-policy-billing"
				err = billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 5, GroupID: &groupID, Group: &Group{ID: groupID, RateMultiplier: 2, ImagePrice1K: &price}}, User: &User{ID: 4}, Account: account})
				require.NoError(t, err)
				require.Equal(t, 1, billingRepo.calls, "image policy reports reuse the original unique ledger")
				require.InDelta(t, 0.4, billingRepo.lastCmd.BalanceCost, 1e-10)
				payload := awaitTaskReport(t, reports)
				require.Equal(t, path, gjson.GetBytes(payload["ctx"], "path").Str)
				require.Equal(t, "0.4", gjson.GetBytes(payload["record"], "cost.usd").String())
				require.EqualValues(t, 1, gjson.GetBytes(payload["record"], "usage.imageCount").Int())
				counts := map[string]int{}
				for _, item := range gjson.GetBytes(payload["record"], "requestPlugins").Array() {
					counts[item.Get("stage").Str]++
				}
				require.Equal(t, map[string]int{"request": 1, "ingress": 1, "outbound": 1, "egress": 1}, counts)
				require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, 5*time.Second, 5*time.Millisecond)
				require.Zero(t, s.TaskReportStats().Failed, "the report hook must finish before its runtime is closed")
			})
		}
	}
}

func TestNativeImagesPoliciesProtectPricingAndFailBeforeIO(t *testing.T) {
	for _, tc := range []struct{ name, stage, hook string }{
		{"reasoning", "request", `return {action:"continue",effects:[{type:"reasoning.set",effort:"high"}]}`},
		{"count", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/n",value:2}]}]}`},
		{"count casing", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/N",value:2}]}]}`},
		{"size", "ingress", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/size",value:"4096x4096"}]}]}`},
		{"future price control", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/frame_count",value:100}]}]}`},
		{"authentication", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{Authorization:"private"}}]}`},
		{"readonly egress", "egress", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/user",value:"illegal"}]}]}`},
		{"reject", "egress", `return {action:"reject",code:"blocked",message:"private rejection"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, account, upstream, plan := nativeImagesPolicyFixture(t, openAIImagesGenerationsEndpoint, providerPolicyMeta+`export function `+tc.stage+`(){`+tc.hook+`}`, []string{tc.stage})
			body := []byte(`{"model":"gpt-image-2","prompt":"draw","n":1,"size":"1024x1024"}`)
			c, response := providerContext(openAIImagesGenerationsEndpoint, string(body))
			c.Request.Header.Set("Content-Type", "application/json")
			parsed, err := s.gateway.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			_, err = s.gateway.ForwardImages(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, parsed, "")
			var failure *RequestPluginFailure
			require.ErrorAs(t, err, &failure)
			require.Empty(t, upstream.requests)
			require.GreaterOrEqual(t, response.Code, 400)
			require.NotContains(t, response.Body.String(), "private")
		})
	}
}

func testMultipartPNGBytes(t *testing.T, minSize int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 220, G: 30, B: 40, A: 255})
	img.Set(1, 1, color.RGBA{R: 40, G: 180, B: 230, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	raw := buf.Bytes()
	if minSize <= len(raw) {
		return raw
	}
	// Insert a valid ancillary tEXt chunk right before the 12-byte IEND chunk
	// so image.DecodeConfig and binary upload validators accept > 1 MiB PNGs.
	const iendLen = 12
	require.Greater(t, len(raw), iendLen)
	padLen := minSize - len(raw) + 64
	chunkData := make([]byte, 0, len("Note\x00")+padLen)
	chunkData = append(chunkData, "Note\x00"...)
	chunkData = append(chunkData, bytes.Repeat([]byte("A"), padLen)...)
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

func buildTestMultipartImagesEdit(t *testing.T, boundary string, fields map[string]string, filename string, pngBytes []byte) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if boundary != "" {
		require.NoError(t, writer.SetBoundary(boundary))
	}
	for _, key := range []string{"model", "prompt", "n", "size"} {
		if val, ok := fields[key]; ok {
			require.NoError(t, writer.WriteField(key, val))
		}
	}
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename=%q`, filename))
	hdr.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(hdr)
	require.NoError(t, err)
	_, err = part.Write(pngBytes)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return body.Bytes(), writer.FormDataContentType()
}

func TestNativeImagesMultipartRequestStagesPreserveBinaryBytesAndReport(t *testing.T) {
	smallPNG := testMultipartPNGBytes(t, 0)
	largePNG := testMultipartPNGBytes(t, MaxRequestPluginBody+64*1024)
	require.Greater(t, len(largePNG), MaxRequestPluginBody)

	for _, tc := range []struct {
		name   string
		oauth  bool
		direct bool
		png    []byte
	}{
		{name: "api-key-small", oauth: false, direct: false, png: smallPNG},
		{name: "api-key-oversized", oauth: false, direct: false, png: largePNG},
		{name: "oauth-direct-small", oauth: true, direct: true, png: smallPNG},
		{name: "oauth-direct-oversized", oauth: true, direct: true, png: largePNG},
		{name: "oauth-bridge-small", oauth: true, direct: false, png: smallPNG},
		{name: "oauth-bridge-oversized", oauth: true, direct: false, png: largePNG},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectedUpstream := "gpt-image-2-vendor"
			if tc.oauth {
				if tc.direct {
					expectedUpstream = "gpt-image-2"
				} else {
					expectedUpstream = "gpt-image-1"
				}
			}
			source := globalHTTPReportSource + fmt.Sprintf(`
export function request(ctx,req){
 if(ctx.format!=="openai" || ctx.path!==%q || ctx.model!=="gpt-image-2" || ctx.upstreamModel!==%q || req.body.prompt!=="draw")throw Error("wrong request stage");
 if(req.body.image!==undefined)throw Error("binary upload leaked into JSON view");
 const items=utils.attachments();
 if(items.length!==1)throw Error("expected 1 multipart image");
 const info=utils.media(items[0].ref);
 if(info.mime!=="image/png" || info.width!==2 || info.height!==2 || info.size!==%d)throw Error("wrong media info: "+JSON.stringify(info));
 return {action:"continue"};
}
export function ingress(ctx,req){
 const bridge=ctx.format==="openai_responses";
 const prompt=bridge?req.body.input[0].content[0].text:req.body.prompt;
 if(prompt!=="draw" || ctx.upstreamModel!==%q)throw Error("wrong late ingress");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:bridge?"/input/0/content/0/text":"/prompt",value:"sanitized"}]}]};
}
export function outbound(ctx,req){
 const bridge=ctx.format==="openai_responses";
 const prompt=bridge?req.body.input[0].content[0].text:req.body.prompt;
 if(prompt!=="sanitized" || req.headers?.Authorization)throw Error("wrong outbound");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/user",value:"policy"}]},{type:"header.patch",set:{"X-Image-Policy":"checked"}}]};
}
export function egress(ctx,req){
 if(req.body.user!=="policy" || req.headers?.["X-Image-Policy"]!=="checked")throw Error("wrong egress");
 return {action:"continue"};
}`, openAIImagesEditsEndpoint, expectedUpstream, len(tc.png), expectedUpstream)

			stages := []string{"request", "ingress", "outbound", "egress", "report"}
			var s *NewAPIPluginService
			var account *Account
			var upstream *generationFixtureHTTP
			var plan *RequestPluginPlan
			if tc.oauth {
				s, account, upstream, plan = nativeImagesOAuthPolicyFixture(t, openAIImagesEditsEndpoint, source, stages, tc.direct)
				nativeImagesOAuthPolicyResponse(upstream, tc.direct, false)
			} else {
				s, account, upstream, plan = nativeImagesPolicyFixture(t, openAIImagesEditsEndpoint, source, stages)
				upstream.response = `{"created":1710000007,"data":[{"b64_json":"ZmluYWw="}],"usage":{"input_tokens":10,"output_tokens":18,"output_tokens_details":{"image_tokens":8}}}`
			}
			reports := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 4)}
			s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: reports}
			s.taskReports = newTaskReportExecutor(s)
			t.Cleanup(s.taskReports.close)

			rawBody, contentType := buildTestMultipartImagesEdit(t, "test-img-boundary", map[string]string{
				"model":  "gpt-image-2",
				"prompt": "draw",
				"n":      "1",
				"size":   "1024x1024",
			}, "source.png", tc.png)
			c, response := providerContext(openAIImagesEditsEndpoint, "")
			c.Request = httptestNewMultipartRequest(openAIImagesEditsEndpoint, rawBody, contentType)
			parsed, err := s.gateway.ParseOpenAIImagesRequest(c, rawBody)
			require.NoError(t, err)

			ctx := WithRequestPluginPlan(c.Request.Context(), plan)
			report := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "img-user")
			require.NotNil(t, report)
			c.Writer = report

			result, err := s.gateway.ForwardImages(ctx, c, account, rawBody, parsed, "")
			require.NoError(t, err, "runs: %+v", s.repo.(*requestPluginFixtureRepo).runs)
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, 1, result.ImageCount)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "checked", upstream.requests[0].Header.Get("X-Image-Policy"))

			sent, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			if !tc.oauth {
				mediaType, params, err := mime.ParseMediaType(upstream.requests[0].Header.Get("Content-Type"))
				require.NoError(t, err)
				require.Equal(t, "multipart/form-data", mediaType)
				reader := multipart.NewReader(bytes.NewReader(sent), params["boundary"])
				gotFields := map[string]string{}
				var gotImage []byte
				for {
					part, err := reader.NextRawPart()
					if errors.Is(err, io.EOF) {
						break
					}
					require.NoError(t, err)
					data, readErr := io.ReadAll(part)
					_ = part.Close()
					require.NoError(t, readErr)
					if part.FileName() != "" {
						gotImage = data
					} else {
						gotFields[part.FormName()] = string(data)
					}
				}
				require.Equal(t, "gpt-image-2-vendor", gotFields["model"])
				require.Equal(t, "sanitized", gotFields["prompt"])
				require.Equal(t, "policy", gotFields["user"])
				require.Equal(t, tc.png, gotImage, "multipart binary upload must stay byte-for-byte identical")
			} else {
				require.Equal(t, "policy", gjson.GetBytes(sent, "user").Str)
				var dataURL string
				if tc.direct {
					require.Equal(t, "sanitized", gjson.GetBytes(sent, "prompt").Str)
					dataURL = gjson.GetBytes(sent, "images.0.image_url").Str
				} else {
					require.Equal(t, "sanitized", gjson.GetBytes(sent, "input.0.content.0.text").Str)
					for _, item := range gjson.GetBytes(sent, "input.0.content").Array() {
						if item.Get("type").Str == "input_image" {
							dataURL = item.Get("image_url").Str
						}
					}
				}
				_, encoded, ok := strings.Cut(dataURL, ",")
				require.True(t, ok)
				decoded, err := base64.StdEncoding.DecodeString(encoded)
				require.NoError(t, err)
				require.Equal(t, tc.png, decoded, "OAuth inline image bytes must remain byte-for-byte identical")
			}

			report.Finish(rawBody)
			require.Empty(t, reports.requests, "HTTP completion alone must wait for confirmed ledger")
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			price, groupID := 0.2, int64(7)
			result.RequestID = "multipart-image-billing-" + tc.name
			err = billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 5, GroupID: &groupID, Group: &Group{ID: groupID, RateMultiplier: 2, ImagePrice1K: &price}}, User: &User{ID: 4}, Account: account})
			require.NoError(t, err)
			require.Equal(t, 1, billingRepo.calls)

			payload := awaitTaskReport(t, reports)
			require.Equal(t, "0.4", gjson.GetBytes(payload["record"], "cost.usd").String())
			require.EqualValues(t, 1, gjson.GetBytes(payload["record"], "usage.imageCount").Int())
			require.Equal(t, "draw", gjson.GetBytes(payload["record"], "request.body.prompt").Str)
			mediaItems := gjson.GetBytes(payload["record"], "request.media").Array()
			require.Len(t, mediaItems, 1)
			require.Equal(t, "image/png", mediaItems[0].Get("mimeType").Str)
			require.EqualValues(t, len(tc.png), mediaItems[0].Get("bytes").Int())
			require.NotContains(t, string(payload["record"]), "Note\x00", "raw binary upload bytes must never enter report")
			counts := map[string]int{}
			for _, item := range gjson.GetBytes(payload["record"], "requestPlugins").Array() {
				counts[item.Get("stage").Str]++
			}
			require.Equal(t, map[string]int{"request": 1, "ingress": 1, "outbound": 1, "egress": 1}, counts)
			require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, 5*time.Second, 5*time.Millisecond)
			require.Zero(t, s.TaskReportStats().Failed)
		})
	}
}

func httptestNewMultipartRequest(path string, body []byte, contentType string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, "http://localhost"+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	return req
}

func TestNativeImagesMultipartGuardsFailBeforeIO(t *testing.T) {
	pngBytes := testMultipartPNGBytes(t, 0)
	for _, oauth := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			stage     string
			hook      string
			onlyAPI   bool
			forbidden bool
		}{
			{name: "mutate-n", stage: "outbound", hook: `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/n",value:"2"}]}]}`},
			{name: "mutate-size", stage: "ingress", hook: `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/size",value:"4096x4096"}]}]}`},
			{name: "mutate-model", stage: "outbound", hook: `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/model",value:"other-model"}]}]}`},
			{name: "add-arbitrary-field", stage: "outbound", hook: `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/extra",value:"bad"}]}]}`},
			{name: "boundary-injection", stage: "outbound", hook: `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/prompt",value:"bad\r\n--guard-boundary\r\n"}]}]}`, onlyAPI: true},
			{name: "reject", stage: "egress", hook: `return {action:"reject",code:"blocked",message:"private rejection"}`, forbidden: true},
		} {
			if tc.onlyAPI && oauth {
				continue
			}
			t.Run(fmt.Sprintf("oauth=%t/%s", oauth, tc.name), func(t *testing.T) {
				source := providerPolicyMeta + `export function ` + tc.stage + `(){` + tc.hook + `}`
				var s *NewAPIPluginService
				var account *Account
				var upstream *generationFixtureHTTP
				var plan *RequestPluginPlan
				if oauth {
					s, account, upstream, plan = nativeImagesOAuthPolicyFixture(t, openAIImagesEditsEndpoint, source, []string{tc.stage}, true)
				} else {
					s, account, upstream, plan = nativeImagesPolicyFixture(t, openAIImagesEditsEndpoint, source, []string{tc.stage})
				}
				rawBody, contentType := buildTestMultipartImagesEdit(t, "guard-boundary", map[string]string{
					"model":  "gpt-image-2",
					"prompt": "draw",
					"n":      "1",
					"size":   "1024x1024",
				}, "source.png", pngBytes)
				c, response := providerContext(openAIImagesEditsEndpoint, "")
				c.Request = httptestNewMultipartRequest(openAIImagesEditsEndpoint, rawBody, contentType)
				parsed, err := s.gateway.ParseOpenAIImagesRequest(c, rawBody)
				require.NoError(t, err)
				_, err = s.gateway.ForwardImages(WithRequestPluginPlan(c.Request.Context(), plan), c, account, rawBody, parsed, "")
				var failure *RequestPluginFailure
				require.ErrorAs(t, err, &failure)
				require.Empty(t, upstream.requests, "invalid or rejected multipart edits must fail before upstream I/O")
				require.GreaterOrEqual(t, response.Code, 400)
				if tc.forbidden {
					require.Equal(t, http.StatusForbidden, response.Code)
				}
				require.NotContains(t, response.Body.String(), "private")
			})
		}
	}
}
