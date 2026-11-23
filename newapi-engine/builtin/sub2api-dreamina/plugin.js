// SPDX-License-Identifier: AGPL-3.0-or-later
// code1024 adapter for the public Sub2API channel documentation (2026-11-21).
// No upstream credentials or observed account data are included.
const official5 = false;
export const meta = {
  apiVersion: 1, key: "sub2api-dreamina", name: "官转二 · Sub2API", version: "1.0.0",
  description: {zh: "支持文生、首帧、首尾帧及多素材；URL 或已上传对象引用。", en: "Sub2API video channel; OpenAI Videos with final-bill reconciliation"},
  author: {name: "code1024"}, fetchMode: "per_task", upstreams: ["vendor"],
  models: ["seedance-2.0", "seedance-2.0-fast", "seedance-2.0-mini", "seedance-2.5-draft", "seedance-2.5-flash", "seedance-pro-fast", "dreamina-seedance-2.5-draft", "dreamina-seedance-2.5-flash"], protocols: ["openai_video"],
  usageSchema: {seconds: {type: "number", unit: "second", description: {zh: "请求视频秒数", en: "Requested video seconds"}}}
};
const root = "/api/v1/video-generations";
function object(value) { return value !== null && typeof value === "object" && !Array.isArray(value); }
function text(value) { return typeof value === "string" ? value.trim() : ""; }
function data(body) {
  if (!object(body) || body.code !== 0 || !object(body.data)) throw new Error("Invalid or rejected upstream response");
  return body.data;
}
function identity(value) {
  const id = typeof value === "number" && Number.isSafeInteger(value) ? String(value) : text(value);
  if (!id || id.length > 512 || !/^[A-Za-z0-9_-]+$/.test(id)) throw new Error("Invalid upstream task ID");
  return id;
}
function headers(ctx) { return {Authorization: "Bearer " + ctx.apiKey, "Content-Type": "application/json"}; }
function endpoint(ctx, id) { return ctx.baseUrl.replace(/\/+$/, "") + root + (id ? "/" + encodeURIComponent(identity(id)) : ""); }
function mediaURL(value) {
  const url = text(value);
  if (!/^https:\/\/[^\s/@?#]+(?:\/[^\s]*)?$/.test(url) || url.length > 8192) throw new Error("Reference must be a public HTTPS URL");
  return url;
}
function decode(ctx) {
  if (!ctx.body || ctx.body.kind !== "json" || !object(ctx.body.value)) throw new Error("JSON video request required");
  const req = ctx.body.value, options = req.metadata === undefined ? {} : req.metadata;
  if (!object(options)) throw new Error("metadata must be an object");
  for (const key of Object.keys(req)) {
    if (!["model", "prompt", "seconds", "duration", "size", "metadata", "input_reference"].includes(key)) throw new Error("Unsupported video field: " + key);
  }
  for (const key of Object.keys(options)) {
    if (!["resolution", "ratio", "task_mode", "content"].includes(key)) throw new Error("Unsupported metadata field: " + key);
  }
  const model = text(ctx.upstreamModel || ctx.model);
  const canonical = model.replace(/^dreamina-/, "");
  if (official5 ? !/^cc-seedance-[a-zA-Z0-9._-]+$/.test(model) : !meta.models.includes(model)) throw new Error("Model is not supported by this channel");
  const prompt = text(req.prompt);
  if (!prompt || prompt.length > 8192) throw new Error("prompt is required and must be at most 8192 characters");
  const duration = req.seconds === undefined ? (req.duration === undefined ? (canonical === "seedance-pro-fast" ? 5 : 4) : req.duration) : req.seconds;
  if (!["number", "string"].includes(typeof duration) || !/^\d+$/.test(String(duration)) || !Number.isSafeInteger(Number(duration)) || Number(duration) < 1 || Number(duration) > 3600) throw new Error("seconds must be a positive integer");
  if (req.seconds !== undefined && req.duration !== undefined && Number(req.seconds) !== Number(req.duration)) throw new Error("Conflicting duration fields");
  const sizes = {"1280x720": ["720p", "16:9"], "720x1280": ["720p", "9:16"], "1920x1080": ["1080p", "16:9"], "1080x1920": ["1080p", "9:16"], "3840x2160": ["4k", "16:9"], "2160x3840": ["4k", "9:16"], "854x480": ["480p", "16:9"], "480x854": ["480p", "9:16"], "720x720": ["720p", "1:1"], "960x720": ["720p", "4:3"], "720x960": ["720p", "3:4"]};
  const size = req.size === undefined ? null : sizes[text(req.size)];
  if (req.size !== undefined && !size) throw new Error("Unsupported size; set metadata.resolution and metadata.ratio for native specifications");
  const resolution = text(options.resolution) || (size ? size[0] : canonical === "seedance-2.5-draft" ? "480p" : "720p");
  const ratio = text(options.ratio) || (size ? size[1] : "16:9");
  if (size && (resolution !== size[0] || ratio !== size[1])) throw new Error("size conflicts with metadata resolution or ratio");
  if (!/^[a-zA-Z0-9.]{1,16}$/.test(resolution) || !/^\d{1,2}:\d{1,2}$/.test(ratio)) throw new Error("Invalid resolution or ratio");
  if (!official5) {
    const range = canonical === "seedance-pro-fast" ? [5, 10] : canonical.startsWith("seedance-2.5-") ? [4, 30] : [4, 15];
    const specs = canonical === "seedance-2.0" ? ["720p", "1080p", "4k"] : canonical === "seedance-2.5-draft" ? ["480p"] : canonical === "seedance-2.5-flash" ? ["480p", "720p"] : canonical === "seedance-pro-fast" ? ["720p", "1080p"] : ["720p"];
    if (Number(duration) < range[0] || Number(duration) > range[1] || !specs.includes(resolution) || !["21:9", "16:9", "4:3", "1:1", "3:4", "9:16"].includes(ratio)) throw new Error("Unsupported model duration, resolution or ratio");
  }
  let content = options.content === undefined ? [] : options.content;
  if (!Array.isArray(content) || content.length > 50) throw new Error("metadata.content must be an array of at most 50 references");
  if (req.input_reference !== undefined) {
    if (content.length) throw new Error("Use either input_reference or metadata.content");
    content = [object(req.input_reference) ? Object.assign({type: "image_url", role: official5 ? "reference_image" : "first_frame"}, req.input_reference) : {type: "image_url", role: "first_frame", url: req.input_reference}];
  }
  const mode = text(options.task_mode) || (content.length ? (!official5 && content.length === 1 && content[0].role === "first_frame" ? "first_frame" : "references") : "text");
  if (!(official5 ? ["text", "references"] : ["text", "first_frame", "first_last_frame", "references"]).includes(mode)) throw new Error("Unsupported task mode");
  if ((mode === "text") !== (content.length === 0)) throw new Error("Task mode does not match references");
  const counts = {image_url: 0, video_url: 0, audio_url: 0};
  content = content.map(function (item) {
    if (!object(item) || !Object.prototype.hasOwnProperty.call(counts, item.type)) throw new Error("Invalid reference type");
    counts[item.type]++;
    for (const key of Object.keys(item)) if (!["type", "role", "storage_object_id", "url"].includes(key)) throw new Error("Unsupported reference field: " + key);
    const role = text(item.role) || ({image_url: "reference_image", video_url: "reference_video", audio_url: "reference_audio"})[item.type];
    const expected = ({image_url: "reference_image", video_url: "reference_video", audio_url: "reference_audio"})[item.type];
    if (mode === "references" ? role !== expected : item.type !== "image_url" || !["first_frame", "last_frame"].includes(role)) throw new Error("Reference role does not match task mode");
    const result = {type: item.type, role: role};
    if (item.storage_object_id !== undefined) {
      const id = text(item.storage_object_id);
      if (!id || id.length > 512 || !/^[a-zA-Z0-9_-]+$/.test(id) || item.url !== undefined) throw new Error("Invalid storage_object_id");
      result.storage_object_id = id;
    } else {
      if (official5) throw new Error("Official channel 5 requires storage_object_id uploaded under the configured upstream Key; URL and inline uploads are unsupported");
      result.url = mediaURL(item.url);
    }
    return result;
  });
  if (mode === "first_frame" && (content.length !== 1 || content[0].role !== "first_frame")) throw new Error("first_frame requires exactly one first frame");
  if (mode === "first_last_frame" && (content.length !== 2 || content[0].role !== "first_frame" || content[1].role !== "last_frame")) throw new Error("Provide first_frame then last_frame");
  if (!official5) {
    const limits = canonical === "seedance-pro-fast" ? [1, 0, 0, 1] : canonical === "seedance-2.5-draft" ? [30, 10, 10, 50] : canonical === "seedance-2.5-flash" ? [10, 5, 5, 20] : [9, 3, 3, 12];
    if (counts.image_url > limits[0] || counts.video_url > limits[1] || counts.audio_url > limits[2] || content.length > limits[3] || (canonical === "seedance-pro-fast" && !["text", "first_frame"].includes(mode))) throw new Error("Reference limits exceeded for model");
    if (canonical.startsWith("seedance-2.0") && counts.audio_url && !counts.image_url && !counts.video_url) throw new Error("Audio references require an image or video reference");
  }
  const requestBody = {model: model, prompt: prompt, duration: Number(duration), resolution: resolution, ratio: ratio, task_mode: mode};
  if (content.length || official5) requestBody.content = content;
  if (official5) requestBody.provider_mode = "standard";
  return {kind: "submit", model: ctx.model, action: content.length ? "image_to_video" : "text_to_video", requestBody: requestBody};
}
export function buildSubmitRequest(ctx) {
  const h = headers(ctx);
  h["X-Request-ID"] = identity(ctx.publicTaskId);
  return {url: endpoint(ctx), method: "POST", headers: h, body: Object.assign({}, ctx.requestBody, {model: ctx.upstreamModel})};
}
export function parseSubmitResponse(ctx, resp) {
  const d = data(resp.body);
  return {taskId: identity(d.id), taskData: resp.body, state: {seconds: ctx.requestBody.duration, resolution: ctx.requestBody.resolution}};
}
export function buildQueryRequest(ctx) { return {url: endpoint(ctx, ctx.taskId), method: "GET", headers: headers(ctx)}; }
function segment(d) { return official5 && d.delivery_kind === "segment"; }
function ready(d) { return !segment(d) && (text(d.video_url) || (object(d.result) && text(d.result.video_url))); }
export function parseTaskResult(ctx, body) {
  const d = data(body);
  if (identity(d.id) !== identity(ctx.taskId)) throw new Error("Upstream returned a different task ID");
  if (d.model !== undefined && d.model !== ctx.upstreamModel) throw new Error("Upstream returned a different model");
  const states = {queued: "QUEUED", running: "IN_PROGRESS", failed: "FAILURE", cancelled: "FAILURE", expired: "FAILURE"};
  let status = states[d.status];
  // Generation and saving the original are separate phases. Segments are a
  // terminal delivery notice, never advertised as the complete final video.
  if (d.status === "succeeded") status = ready(d) || segment(d) ? "SUCCESS" : "IN_PROGRESS";
  return {status: status || "UNKNOWN", progress: String(Math.max(0, Math.min(100, Number(d.progress) || 0))) + "%"};
}
export function extractUsage(ctx) { return {seconds: ctx.requestBody.duration, resolution: ctx.requestBody.resolution}; }
export function extractUsageOnComplete(task, result, body) {
  const d = data(body);
  const facts = {seconds: task.state.seconds, resolution: task.state.resolution, billing_pending: false};
  if (result.status === "FAILURE") return facts;
  // Missing/zero quote is not a final zero bill. Poll the original task until
  // the upstream explicitly settles it and supplies a valid nonnegative bill.
  const amount = (typeof d.final_cost === "number" || typeof d.final_cost === "string") ? String(d.final_cost) : "";
  if (d.billing_status !== "billed" || !/^\d+(?:\.\d{1,8})?$/.test(amount)) {
    facts.billing_pending = true;
    return facts;
  }
  // The public API expresses its balance costs in CNY. A different explicit
  // currency must be reconciled rather than silently converted as CNY.
  facts.upstream_cost = {amount: amount, currency: d.currency || "CNY", unit: "upstream_cost", final: true, source: "sub2api_final_cost"};
  return facts;
}
export function listArtifacts(task) { return task.status === "SUCCESS" && ready(data(task.data)) ? [{key: "video", type: "video"}] : []; }
export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video" || !ready(data(ctx.data))) throw new Error("Final video is unavailable");
  return {url: endpoint(ctx, ctx.upstreamTaskId) + "/content?download=1", method: ctx.clientRequest.method, headers: headers(ctx)};
}
export const protocols = {openai_video: {decodeRequest: decode, render: function (ctx, task) {
  const d = object(task.data) && object(task.data.data) ? task.data.data : {};
  return segment(d) ? {error: {code: "final_video_unavailable", message: "The upstream delivered storyboard segments without a verified complete video. Contact the operator for delivery review."}} : {};
}}};
