// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) code1024 contributors.
// Native Provider host example; this is a local adapter, not an upstream script.
export const meta = {
  apiVersion: 1,
  key: "sub2api-openai-relay",
  name: "OpenAI Compatible Relay",
  version: "1.1.0",
  author: { name: "code1024" },
  description: {
    zh: "通过 Provider 插件转发原生 Chat / Responses 文本及 Embeddings 请求。地址填写上游根路径（路由会追加 /v1）；在账号模型映射中显式配置公开模型及上游模型。使用现有分组定价与日志。",
    en: "Forward native Chat / Responses text and Embeddings requests through the Provider host. Set the base URL to the upstream root (routes append /v1) and explicitly configure account model mappings. Existing group pricing and usage logs apply.",
  },
  relay: [
    { incoming_path: "/v1/chat/completions", upstream_path: "/v1/chat/completions", converter: "none" },
    { incoming_path: "/v1/responses", upstream_path: "/v1/responses", converter: "none" },
    { incoming_path: "/v1/embeddings", upstream_path: "/v1/embeddings", converter: "none" },
  ],
};
