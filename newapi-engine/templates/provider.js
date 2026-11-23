// SPDX-License-Identifier: AGPL-3.0-or-later
// Fictional JSON-only upstream. Replace its fields using actual vendor docs.
// Never embed credentials; accounts supply their own connection and API Key.
export const meta = {
  apiVersion: 1,
  key: "example-plugin-key",
  name: "Example text provider",
  version: "1.0.0",
  author: { name: "Community developer" },
  models: ["example-text"],
  relay: [{ incoming_path: "/v1/chat/completions", upstream_path: "/generate", converter: "plugin", models: ["example-text"] }],
};

export function convertRequest(ctx, request) {
  if (request.stream) throw new Error("This example supports non-streaming requests only");
  return { body: { deployment: request.model, messages: request.body.messages || [] }, state: { model: request.model } };
}

export function convertResponse(ctx, response) {
  if (response.status < 200 || response.status >= 300) {
    return { error: { status: response.status >= 400 ? response.status : 502, message: "Upstream request failed", type: "upstream_error" } };
  }
  const result = JSON.parse(response.body);
  return { body: { id: result.id, created: utils.unixNow(), object: "chat.completion", model: ctx.state.model,
    choices: [{ index: 0, message: { role: "assistant", content: result.text }, finish_reason: "stop" }],
    usage: { prompt_tokens: result.input_tokens, completion_tokens: result.output_tokens, total_tokens: result.input_tokens + result.output_tokens } } };
}

export function convertStreamEvent() {
  return { error: { status: 502, message: "Streaming is not implemented by this example" } };
}
