// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) code1024 contributors.
// Local OpenAI Videos adapter. A protocol claim is not proof of upstream support.
export const meta = {
  apiVersion: 1,
  key: "sub2api-openai-video",
  name: "OpenAI Compatible Video",
  version: "1.0.1",
  author: { name: "code1024" },
  description: {
    zh: "接入 OpenAI Videos 兼容上游或 New API 视频网关。地址可填根路径或 /v1；必须在账号模型映射中配置具体模型。默认以 multipart 提交，也可选择 JSON。任务与计费由本站管理。",
    en: "Connect an OpenAI Videos compatible upstream or New API video gateway. Use an origin or /v1 base URL and explicit account model mappings. Submit multipart by default, or select JSON. The host owns tasks and billing.",
  },
  models: ["*"],
  protocols: ["openai_video"],
  fetchMode: "per_task",
  upstreams: ["vendor", "new_api"],
  channelConfig: {
    type: "object",
    properties: {
      submission_format: {
        type: "string",
        title: { zh: "上游提交格式", en: "Upstream submission format" },
        enum: ["multipart", "json"],
        default: "multipart",
        description: {
          zh: "OpenAI Videos 使用 multipart；仅当上游明确支持 JSON 时切换。文件上传始终使用 multipart。",
          en: "Use multipart for OpenAI Videos. Select JSON only when the upstream supports it. File uploads always use multipart.",
        },
      },
    },
  },
  usageSchema: {
    seconds: {
      type: "number",
      unit: "second",
      description: { zh: "视频秒数", en: "Video duration in seconds" },
    },
  },
};

function videoURL(ctx, taskID, content) {
  const base = String(ctx.baseUrl || "").replace(/\/+$/, "");
  const path = base + (/\/v1$/.test(base) ? "/videos" : "/v1/videos");
  return taskID === undefined ? path : path + "/" + encodeURIComponent(taskID) + (content ? "/content" : "");
}

function seconds(value) {
  const duration = Number(value);
  if (!["number", "string"].includes(typeof value) || !Number.isFinite(duration) || duration <= 0 || duration > 3600)
    throw new Error("seconds must be greater than 0 and at most 3600");
  return duration;
}

function normalizeRequest(value, model) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("request body must be an object");
  if (typeof value.prompt !== "string" || !value.prompt.trim()) throw new Error("prompt is required");
  const body = Object.assign({}, value, { model: model });
  if (body.seconds !== undefined && body.duration !== undefined && seconds(body.seconds) !== seconds(body.duration))
    throw new Error("seconds and duration must agree");
  if (body.seconds === undefined && body.duration !== undefined) body.seconds = body.duration;
  if (body.seconds !== undefined) body.seconds = String(seconds(body.seconds));
  delete body.duration;
  if (body.metadata !== undefined && (!body.metadata || typeof body.metadata !== "object" || Array.isArray(body.metadata)))
    throw new Error("metadata must be an object");
  return body;
}

export function buildSubmitRequest(ctx) {
  const body = normalizeRequest(ctx.requestBody, ctx.upstreamModel);
  const headers = { Authorization: "Bearer " + ctx.apiKey };
  const files = ctx.files || [];
  if (files.length && body.input_reference !== undefined) throw new Error("input_reference cannot be both a field and a file");
  if ((ctx.config || {}).submission_format === "json" && files.length === 0) {
    headers["Content-Type"] = "application/json";
    return { url: videoURL(ctx), method: "POST", headers: headers, body: body };
  }
  const parts = [];
  for (const name of Object.keys(body)) {
    let value = body[name];
    if (value === undefined || value === null) continue;
    if (name === "metadata") value = JSON.stringify(value);
    if (typeof value === "object") throw new Error("multipart field must be scalar: " + name);
    parts.push({ name: name, value: value });
  }
  if (files.length > 1 || files.some(file => file.field !== "input_reference")) throw new Error("only one input_reference file is supported");
  for (const file of files) parts.push({ name: "input_reference", fileRef: file.ref, filename: file.filename });
  return { url: videoURL(ctx), method: "POST", headers: headers, bodyType: "multipart", parts: parts };
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body || {};
  if (typeof body.id !== "string" || !body.id.trim()) throw new Error("upstream video id is required");
  return { taskId: body.id, taskData: body };
}

export function extractUsage(ctx) {
  const value = (ctx.requestBody || {}).seconds;
  return value === undefined ? {} : { seconds: seconds(value) };
}

export function extractUsageOnComplete(_task, result, body) {
  if (result.status !== "SUCCESS" || !body || body.seconds === undefined) return {};
  // Missing or invalid final usage must not prevent saving a terminal task.
  // The host retains the hold for review instead of billing the estimate.
  try {
    return { seconds: seconds(body.seconds) };
  } catch (_error) {
    return {};
  }
}

export function buildQueryRequest(ctx) {
  return { url: videoURL(ctx, ctx.taskId), method: "GET", headers: { Authorization: "Bearer " + ctx.apiKey } };
}

export function parseTaskResult(_ctx, body) {
  const statuses = { queued: "QUEUED", pending: "QUEUED", in_progress: "IN_PROGRESS", processing: "IN_PROGRESS", completed: "SUCCESS", failed: "FAILURE", cancelled: "FAILURE" };
  const status = statuses[body.status];
  const result = { status: status || "UNKNOWN" };
  if (!status) result.reason = "unrecognized video status";
  if (status === "FAILURE") result.reason = "upstream video generation failed";
  const progress = Number(body.progress);
  if (Number.isFinite(progress) && progress >= 0 && progress <= 100) result.progress = progress + "%";
  return result;
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  return {
    url: videoURL(ctx, ctx.upstreamTaskId, true),
    method: ctx.clientRequest.method,
    headers: { Authorization: "Bearer " + ctx.apiKey },
  };
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const envelope = ctx.body || {};
      let value = envelope.value;
      let hasFile = false;
      if (envelope.kind === "multipart") {
        value = {};
        for (const name of Object.keys(envelope.fields || {})) {
          const values = envelope.fields[name];
          if (values.length !== 1) throw new Error("field must be provided once: " + name);
          value[name] = values[0];
        }
        if (value.metadata !== undefined) {
          try { value.metadata = JSON.parse(value.metadata); } catch (_error) { throw new Error("metadata must be a JSON object string"); }
        }
        const files = envelope.files || [];
        if (files.length > 1 || files.some(file => file.field !== "input_reference")) throw new Error("only one input_reference file is supported");
        if (files.length && value.input_reference !== undefined) throw new Error("input_reference cannot be both a field and a file");
        hasFile = files.length > 0;
      } else if (envelope.kind !== "json") {
        throw new Error("JSON or multipart body required");
      }
      const body = normalizeRequest(value, ctx.model);
      return { kind: "submit", model: ctx.model, action: hasFile || body.input_reference ? "image_to_video" : "text_to_video", requestBody: body };
    },
    render: function (_ctx, task) {
      const statuses = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "in_progress", SUCCESS: "completed", FAILURE: "failed" };
      const progress = Number(String(task.progress || "0").replace("%", ""));
      const result = {
        id: task.task_id,
        object: "video",
        model: (task.properties || {}).origin_model_name || "",
        status: statuses[task.status] || "unknown",
        progress: Number.isFinite(progress) ? Math.min(100, Math.max(0, progress)) : 0,
        created_at: Number(task.created_at || 0),
      };
      if (task.status === "SUCCESS" || task.status === "FAILURE") {
        const finished = Number(task.finished_at || task.updated_at || 0);
        if (finished > 0) result.completed_at = finished;
      }
      if (task.status === "FAILURE") result.error = { code: "video_generation_failed", message: "The video generation task failed." };
      return result;
    },
  },
};
