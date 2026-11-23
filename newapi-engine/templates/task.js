// SPDX-License-Identifier: AGPL-3.0-or-later
// Fictional video upstream: POST /jobs, GET /jobs/:id. Replace vendor fields.
// The host owns routing, pricing, persistent tasks, credentials and settlement.
export const meta = {
  apiVersion: 1,
  key: "example-plugin-key",
  name: "Example video task",
  version: "1.0.0",
  author: { name: "Community developer" },
  models: ["example-video"],
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: { seconds: { type: "number", unit: "second", description: { en: "Video seconds", zh: "视频秒数" } } },
};

export const protocols = {
  openai_video: {
    decodeRequest(ctx) {
      if (!ctx.body || ctx.body.kind !== "json" || !ctx.body.value) throw new Error("JSON body required");
      const body = ctx.body.value;
      if (typeof body.prompt !== "string" || !body.prompt.trim()) throw new Error("prompt required");
      if (body.seconds !== undefined && typeof body.seconds !== "number" && typeof body.seconds !== "string") throw new Error("seconds must be numeric");
      const seconds = body.seconds === undefined ? 8 : Number(body.seconds);
      if (!Number.isFinite(seconds) || seconds <= 0 || seconds > 60) throw new Error("seconds must be between 1 and 60");
      return { kind: "submit", model: ctx.model, action: "text_to_video", requestBody: { model: ctx.model, prompt: body.prompt, seconds } };
    },
    render(ctx, task) {
      const statuses = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "in_progress", SUCCESS: "completed", FAILURE: "failed" };
      return { id: task.task_id, object: "video", status: statuses[task.status] || "unknown", model: (task.properties || {}).origin_model_name || "example-video" };
    },
  },
};

export function buildSubmitRequest(ctx) {
  return { url: ctx.baseUrl + "/jobs", method: "POST", headers: { Authorization: "Bearer " + ctx.apiKey, "Content-Type": "application/json" },
    body: { model: ctx.upstreamModel, prompt: ctx.requestBody.prompt, seconds: ctx.requestBody.seconds } };
}
export function parseSubmitResponse(ctx, response) {
  if (!response.body || typeof response.body.id !== "string") throw new Error("upstream task id required");
  return { taskId: response.body.id, taskData: {} };
}
export function extractUsage(ctx) { return { seconds: Number(ctx.requestBody.seconds || 8) }; }
export function buildQueryRequest(ctx) {
  return { url: ctx.baseUrl + "/jobs/" + encodeURIComponent(ctx.taskId), method: "GET", headers: { Authorization: "Bearer " + ctx.apiKey } };
}
export function parseTaskResult(ctx, body) {
  if (body.status === "completed") return { status: "SUCCESS", progress: "100%", url: body.video_url };
  if (body.status === "failed") return { status: "FAILURE", reason: "Upstream generation failed" };
  if (body.status === "processing") return { status: "IN_PROGRESS" };
  if (body.status === "queued") return { status: "QUEUED" };
  return { status: "UNKNOWN" };
}
export function extractUsageOnComplete(task, result, body) {
  const seconds = body.seconds;
  return typeof seconds === "number" && Number.isFinite(seconds) && seconds >= 0 ? { seconds } : {};
}
export function listArtifacts(task) { return task.status === "SUCCESS" ? [{ key: "video", type: "video" }] : []; }
export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  return { url: ctx.baseUrl + "/jobs/" + encodeURIComponent(ctx.upstreamTaskId) + "/content", method: ctx.clientRequest.method, headers: { Authorization: "Bearer " + ctx.apiKey } };
}
