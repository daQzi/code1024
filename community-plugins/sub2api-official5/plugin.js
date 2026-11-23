// SPDX-License-Identifier: AGPL-3.0-or-later
// Standalone community plugin for 官转五 (Sub2API Official-5 video channel).
// Target API specification: https://sub2api.0x0.fan/official-5/api (2026-11-21).
// No upstream credentials or observed account data are included.

export const meta = {
  apiVersion: 1,
  key: "community-sub2api-official5",
  name: "官转五 · 社区插件",
  version: "1.1.0",
  description: {
    zh: "独立社区版官转五视频插件：支持 cc-seedance-* 系列、已上传 storage_object_id / client_ref_id 与分段交付提示，按终态账单核对结算。",
    en: "Standalone community plugin for Sub2API Official-5 video channel (cc-seedance-*) with storage object references, segment notices, and final-bill reconciliation."
  },
  author: { name: "code1024" },
  fetchMode: "per_task",
  upstreams: ["vendor"],
  models: ["cc-seedance-*"],
  protocols: ["openai_video"],
  usageSchema: {
    seconds: {
      type: "number",
      unit: "second",
      description: { zh: "请求视频秒数", en: "Requested video seconds" }
    }
  }
};

const root = "/api/v1/video-generations";
const sizeSpecs = {
  "1280x720": ["720p", "16:9"],
  "720x1280": ["720p", "9:16"],
  "1920x1080": ["1080p", "16:9"],
  "1080x1920": ["1080p", "9:16"],
  "3840x2160": ["4k", "16:9"],
  "2160x3840": ["4k", "9:16"],
  "854x480": ["480p", "16:9"],
  "480x854": ["480p", "9:16"],
  "720x720": ["720p", "1:1"],
  "960x720": ["720p", "4:3"],
  "720x960": ["720p", "3:4"]
};

function object(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function text(value) {
  return typeof value === "string" ? value.trim() : "";
}

function data(body) {
  if (!object(body) || body.code !== 0 || !object(body.data)) {
    throw new Error("Invalid or rejected upstream response");
  }
  return body.data;
}

function identity(value) {
  const id = typeof value === "number" && Number.isSafeInteger(value) ? String(value) : text(value);
  if (!id || id.length > 512 || !/^[A-Za-z0-9_-]+$/.test(id)) {
    throw new Error("Invalid upstream task ID");
  }
  return id;
}

function headers(ctx) {
  return { Authorization: "Bearer " + ctx.apiKey, "Content-Type": "application/json" };
}

function endpoint(ctx, id) {
  return ctx.baseUrl.replace(/\/+$/, "") + root + (id ? "/" + encodeURIComponent(identity(id)) : "");
}

function decode(ctx) {
  if (!ctx.body || !["json", "multipart"].includes(ctx.body.kind)) {
    throw new Error("JSON or multipart video request required");
  }
  let req = ctx.body.value;
  const boundFiles = ctx.body.kind === "multipart" ? (ctx.body.files || []) : [];
  if (ctx.body.kind === "multipart") {
    req = {};
    for (const key of Object.keys(ctx.body.fields || {})) {
      const values = ctx.body.fields[key];
      if (!Array.isArray(values) || values.length !== 1) {
        throw new Error("Text fields must be provided once");
      }
      req[key] = values[0];
    }
    if (req.metadata !== undefined) {
      req.metadata = JSON.parse(req.metadata);
    }
  }
  if (!object(req)) {
    throw new Error("JSON video object required");
  }
  const options = req.metadata === undefined ? {} : req.metadata;
  if (!object(options)) {
    throw new Error("metadata must be an object");
  }
  for (const key of Object.keys(req)) {
    if (!["model", "prompt", "seconds", "duration", "size", "metadata", "input_reference"].includes(key)) {
      throw new Error("Unsupported video field: " + key);
    }
  }
  for (const key of Object.keys(options)) {
    if (!["resolution", "ratio", "task_mode", "content", "reference_durations", "selected_reference_ids"].includes(key)) {
      throw new Error("Unsupported metadata field: " + key);
    }
  }

  const model = text(ctx.upstreamModel || ctx.model);
  const canonical = model.replace(/^dreamina-/, "");
  if (!/^cc-seedance-[a-zA-Z0-9._-]+$/.test(model)) {
    throw new Error("Model is not supported by this channel");
  }
  const prompt = text(req.prompt);
  if (!prompt || prompt.length > 8192) {
    throw new Error("prompt is required and must be at most 8192 characters");
  }

  const duration = req.seconds === undefined
    ? (req.duration === undefined ? (canonical === "seedance-pro-fast" ? 5 : 4) : req.duration)
    : req.seconds;
  if (!["number", "string"].includes(typeof duration) || !/^\d+$/.test(String(duration)) || !Number.isSafeInteger(Number(duration)) || Number(duration) < 1 || Number(duration) > 3600) {
    throw new Error("seconds must be a positive integer");
  }
  if (req.seconds !== undefined && req.duration !== undefined && Number(req.seconds) !== Number(req.duration)) {
    throw new Error("Conflicting duration fields");
  }

  const size = req.size === undefined ? null : sizeSpecs[text(req.size)];
  if (req.size !== undefined && !size) {
    throw new Error("Unsupported size; set metadata.resolution and metadata.ratio for native specifications");
  }
  const resolution = text(options.resolution) || (size ? size[0] : (canonical === "seedance-2.5-draft" ? "480p" : "720p"));
  const ratio = text(options.ratio) || (size ? size[1] : "16:9");
  if (size && (resolution !== size[0] || ratio !== size[1])) {
    throw new Error("size conflicts with metadata resolution or ratio");
  }
  if (!/^[a-zA-Z0-9.]{1,16}$/.test(resolution) || !/^\d{1,2}:\d{1,2}$/.test(ratio)) {
    throw new Error("Invalid resolution or ratio");
  }

  let content = options.content === undefined ? [] : options.content;
  if (!Array.isArray(content) || content.length > 50) {
    throw new Error("metadata.content must be an array of at most 50 references");
  }
  if (boundFiles.length) {
    if (content.length || req.input_reference !== undefined) {
      throw new Error("Use file references or existing upstream objects, not both");
    }
    const fieldCounts = {};
    const durations = options.reference_durations || {};
    if (!object(durations)) {
      throw new Error("reference_durations must be an object keyed by file field");
    }
    const fieldKinds = {
      input_reference: "image",
      reference_image: "image",
      "image[]": "image",
      reference_video: "video",
      "video[]": "video",
      reference_audio: "audio",
      "audio[]": "audio"
    };
    content = boundFiles.map(function (file) {
      const kind = fieldKinds[file.field];
      if (!kind || !text(file.ref) || !text(file.mimeType).startsWith(kind + "/")) {
        throw new Error("Invalid reference file field or MIME type");
      }
      const index = fieldCounts[file.field] || 0;
      fieldCounts[file.field] = index + 1;
      const values = durations[file.field];
      const fileDur = Array.isArray(values) ? values[index] : (index === 0 ? values : undefined);
      if (kind !== "image" && (typeof fileDur !== "number" || !Number.isFinite(fileDur) || fileDur <= 0 || fileDur > 86400)) {
        throw new Error("Video and audio references require their actual duration in metadata.reference_durations");
      }
      const item = { type: kind + "_url", role: "reference_" + kind, file_ref: file.ref };
      if (kind !== "image") {
        item.duration_seconds = fileDur;
      }
      return item;
    });
  } else if (options.reference_durations !== undefined) {
    throw new Error("reference_durations requires bound files");
  }

  if (req.input_reference !== undefined) {
    if (content.length) {
      throw new Error("Use either input_reference or metadata.content");
    }
    content = [object(req.input_reference)
      ? Object.assign({ type: "image_url", role: "reference_image" }, req.input_reference)
      : { type: "image_url", role: "reference_image", url: req.input_reference }];
  }

  const mode = text(options.task_mode) || (content.length ? "references" : "text");
  if (!["text", "references"].includes(mode)) {
    throw new Error("Unsupported task mode");
  }
  if ((mode === "text") !== (content.length === 0)) {
    throw new Error("Task mode does not match references");
  }

  const expectedRoles = { image_url: "reference_image", video_url: "reference_video", audio_url: "reference_audio" };
  content = content.map(function (item) {
    if (!object(item) || !Object.prototype.hasOwnProperty.call(expectedRoles, item.type)) {
      throw new Error("Invalid reference type");
    }
    for (const key of Object.keys(item)) {
      if (!["type", "role", "storage_object_id", "url", "client_ref_id", "file_ref", "duration_seconds"].includes(key)) {
        throw new Error("Unsupported reference field: " + key);
      }
    }
    const expectedRole = expectedRoles[item.type];
    const role = text(item.role) || expectedRole;
    if (role !== expectedRole) {
      throw new Error("Reference role does not match task mode");
    }
    const result = { type: item.type, role: role };
    if (item.client_ref_id !== undefined) {
      const refId = text(item.client_ref_id);
      if (!/^[a-zA-Z0-9_-]{1,128}$/.test(refId)) {
        throw new Error("Invalid client_ref_id");
      }
      result.client_ref_id = refId;
    }
    if (item.file_ref !== undefined) {
      if (!boundFiles.some(function (file) { return file.ref === item.file_ref; }) || item.storage_object_id !== undefined || item.url !== undefined) {
        throw new Error("Unbound reference file");
      }
      result.file_ref = item.file_ref;
      if (item.duration_seconds !== undefined) {
        result.duration_seconds = item.duration_seconds;
      }
    } else if (item.storage_object_id !== undefined) {
      const id = text(item.storage_object_id);
      if (!id || id.length > 512 || !/^[a-zA-Z0-9_-]+$/.test(id) || item.url !== undefined) {
        throw new Error("Invalid storage_object_id");
      }
      result.storage_object_id = id;
    } else {
      throw new Error("Official channel 5 requires storage_object_id uploaded under the configured upstream Key; URL and inline uploads are unsupported");
    }
    return result;
  });

  const requestBody = {
    model: model,
    prompt: prompt,
    duration: Number(duration),
    resolution: resolution,
    ratio: ratio,
    task_mode: mode,
    content: content,
    provider_mode: "standard"
  };

  const ids = content
    .filter(function (item) { return item.client_ref_id !== undefined; })
    .map(function (item) { return item.client_ref_id; });
  if (new Set(ids).size !== ids.length) {
    throw new Error("client_ref_id must be unique within the task");
  }
  if (options.selected_reference_ids !== undefined) {
    if (!Array.isArray(options.selected_reference_ids) ||
      options.selected_reference_ids.length > content.length ||
      options.selected_reference_ids.some(function (id) { return !ids.includes(id); }) ||
      new Set(options.selected_reference_ids).size !== options.selected_reference_ids.length) {
      throw new Error("selected_reference_ids must select unique content client_ref_id values");
    }
    requestBody.selected_reference_ids = options.selected_reference_ids;
  }

  return {
    kind: "submit",
    model: ctx.model,
    action: content.length ? "image_to_video" : "text_to_video",
    requestBody: requestBody
  };
}

export function buildSubmitRequest(ctx) {
  const h = headers(ctx);
  h["X-Request-ID"] = identity(ctx.publicTaskId);
  return {
    url: endpoint(ctx),
    method: "POST",
    headers: h,
    body: Object.assign({}, ctx.requestBody, { model: ctx.upstreamModel })
  };
}

export function parseSubmitResponse(ctx, resp) {
  const d = data(resp.body);
  return {
    taskId: identity(d.id),
    taskData: resp.body,
    state: { seconds: ctx.requestBody.duration, resolution: ctx.requestBody.resolution }
  };
}

export function buildQueryRequest(ctx) {
  return { url: endpoint(ctx, ctx.taskId), method: "GET", headers: headers(ctx) };
}

function segment(d) {
  return d.delivery_kind === "segment";
}

function ready(d) {
  return !segment(d) && Boolean(text(d.video_url) || (object(d.result) && text(d.result.video_url)));
}

export function parseTaskResult(ctx, body) {
  const d = data(body);
  if (identity(d.id) !== identity(ctx.taskId)) {
    throw new Error("Upstream returned a different task ID");
  }
  if (d.model !== undefined && d.model !== ctx.upstreamModel) {
    throw new Error("Upstream returned a different model");
  }
  const states = {
    queued: "QUEUED",
    running: "IN_PROGRESS",
    failed: "FAILURE",
    cancelled: "FAILURE",
    expired: "FAILURE"
  };
  let status = states[d.status];
  if (d.status === "succeeded") {
    status = ready(d) || segment(d) ? "SUCCESS" : "IN_PROGRESS";
  }
  return {
    status: status || "UNKNOWN",
    progress: String(Math.max(0, Math.min(100, Number(d.progress) || 0))) + "%"
  };
}

export function extractUsage(ctx) {
  return { seconds: ctx.requestBody.duration, resolution: ctx.requestBody.resolution };
}

export function extractUsageOnComplete(task, result, body) {
  const d = data(body);
  const facts = {
    seconds: task.state.seconds,
    resolution: task.state.resolution,
    billing_pending: false
  };
  if (result.status === "FAILURE") {
    return facts;
  }
  const amount = (typeof d.final_cost === "number" || typeof d.final_cost === "string") ? String(d.final_cost) : "";
  if (d.billing_status !== "billed" || !/^\d+(?:\.\d{1,8})?$/.test(amount)) {
    facts.billing_pending = true;
    return facts;
  }
  facts.upstream_cost = {
    amount: amount,
    currency: d.currency || "CNY",
    unit: "upstream_cost",
    final: true,
    source: "sub2api_final_cost"
  };
  return facts;
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && ready(data(task.data)) ? [{ key: "video", type: "video" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video" || !ready(data(ctx.data))) {
    throw new Error("Final video is unavailable");
  }
  return {
    url: endpoint(ctx, ctx.upstreamTaskId) + "/content?download=1",
    method: ctx.clientRequest.method,
    headers: headers(ctx)
  };
}

export const protocols = {
  openai_video: {
    decodeRequest: decode,
    render: function (ctx, task) {
      const d = object(task.data) && object(task.data.data) ? task.data.data : {};
      return segment(d)
        ? {
            error: {
              code: "final_video_unavailable",
              message: "The upstream delivered storyboard segments without a verified complete video. Contact the operator for delivery review."
            }
          }
        : {};
    }
  }
};
