# Provider 协议转换插件

更新日期：2026-11-22。契约固定于 New API `48f9df96ea7c68eae04a0f3687869f232f48929f`。

Provider 插件相当于一个“翻译员”：下游发送标准 OpenAI 请求，插件把它翻译成上游的字段，再把上游结果翻译回来。账号负责连接、分组、模型映射、并发和原有文本定价；插件不再建立一套用户余额或文本账本。

## 使用顺序

1. 通过插件中心安装固定版本并启用；社区插件按现有投稿、审核、发布流程进入市场。
2. 在 OpenAI 平台的普通 API Key 账号中选择插件，填写上游根地址、Key 和插件配置。
3. 配置公开模型到上游模型的映射，加入对应分组，并按既有方式设置文本价格与倍率。
4. 下游使用本站分组 API Key，调用插件明确声明的 `/v1/chat/completions`、`/v1/responses` 或 `/v1/embeddings`。

仅转发 OpenAI 兼容上游时，选择内置 `sub2api-openai-relay` 即可。不同字段、目标地址或签名规则用自定义 Provider。视频异步生成继续使用 Task 插件与任务日志；这里的文本 Provider 不替换视频任务核心。

## 实际执行顺序

```text
既有入口处理和权限 → 选择账号 → 模型映射、策略、流式 usage 参数
→ 固定版本和配置 → exchangeKey（声明时，共享有界缓存）→ convertRequest（仅 plugin 路由）
→ prepareRequest（有 JSON 正文时）→ 写入路由认证 → signRequest
→ 校验目标和头 → 调用上游 → 检查凭据拒绝与 convertResponse
→ next 时仅重建后续步骤的目标、认证和签名，或选择最终 JSON / SSE
→ 凭据被拒绝时全链最多续期一次，只重试当前步骤
→ 现有 OpenAI 解析、日志、分组价格和账本
```

路由按原始声明顺序取第一个匹配项；前面的不支持转换器不能跳到后面的原生 catch-all。`ChannelRelayConfig` 会剥离 converter 标识，所以宿主同时检查原始路由，确保 `plugin` 实际执行转换。

每次请求固定源码、校验后的配置、入口和 `convertRequest` 返回的 state。响应时不重新读取默认版本或账号配置。一个请求开始后切换版本或停用插件，只影响新请求。流钩子可以返回新 state；未返回时沿用旧 state。不要使用 JS 全局变量保存请求进度：调用可能分配到不同 worker。

## 开发模板

下面是字段映射模板，假设上游 `/generate` 接受 `{deployment, prompt}` 并返回 `{id, text, input_tokens, output_tokens}`。这些字段是示例，需按真实上游文档替换，不代表某个厂商已完成适配。模板只提供非流式生成，流式请求在发往上游前明确拒绝。

```js
export const meta = {
  apiVersion: 1,
  key: "example-text-provider",
  name: "示例文本协议转换",
  version: "1.0.0",
  author: { name: "示例开发者" },
  models: ["vendor-text"],
  relay: [{
    incoming_path: "/v1/chat/completions",
    upstream_path: "/generate",
    converter: "plugin",
    models: ["vendor-text"],
    auth: { type: "header", name: "X-API-Key", value: "{api_key}" },
  }],
};

export function convertRequest(ctx, request) {
  if (request.stream) throw new Error("本模板仅支持非流式请求");
  const messages = request.body.messages || [];
  return {
    body: { deployment: request.model, prompt: messages },
    state: { model: request.model },
  };
}

export function prepareRequest(ctx, request) {
  return { effects: [{ type: "header.patch", set: { "X-Client": "sub2api" } }] };
}

export function convertResponse(ctx, response) {
  if (response.status < 200 || response.status >= 300) {
    return { error: { status: response.status >= 400 ? response.status : 502,
      message: "上游请求失败", type: "upstream_error" } };
  }
  const result = JSON.parse(response.body);
  return { body: {
    id: result.id,
    created: Math.floor(Date.now() / 1000),
    object: "chat.completion",
    model: ctx.state.model,
    choices: [{ index: 0, message: { role: "assistant", content: result.text }, finish_reason: "stop" }],
    usage: { prompt_tokens: result.input_tokens, completion_tokens: result.output_tokens,
      total_tokens: result.input_tokens + result.output_tokens },
  } };
}

export function convertStreamEvent() {
  return { error: { status: 502, message: "本模板未实现流式转换" } };
}
```

纯 `plugin` 路由需要导出三个 convert 钩子。`prepareRequest`、`signRequest` 可选。响应 `usage` 必须来自上游真实用量，并转换成所选客户端协议的标准字段；不要把估算当作实际用量。初始计费模型取账号映射，最终结算继续遵循现有分组价格、倍率和响应模型核验等规则；插件不新增独立的价格逻辑。

## 已支持与限制

| 功能 | 当前行为 |
| --- | --- |
| 入口 | OpenAI 平台普通 API Key 的 Chat Completions / Responses 文本 JSON/SSE，以及 Embeddings JSON；保留原生 `none` 路由 |
| 请求转换 | `converter: plugin`；JSON 正文、method、path/url、业务 headers、JSON contentType、state |
| 准备 | JSON Patch 与业务 header patch；原子生效；可改 path/url；不会读取或修改路由认证头 |
| 签名 | 在最终目标、认证和实际正文确定后调用，摘要是实际发送字节的 SHA256 |
| JSON 响应 | `convertResponse` 输出标准客户端 JSON；`error` 进入现有 HTTP 错误/重试链；`nativeResponse` 可保留成功的原生 OpenAI 响应 |
| 流响应 | 上游 SSE 或 NDJSON → `convertStreamEvent` → 标准 SSE 或插件明确构造的 JSON；支持 state、事件名称、多行 data、干净 EOF 的一次 done 回调 |
| 网络 | 普通 relay 的所有 JS hooks 不提供 `utils.fetch`；只由宿主发送经过校验的上游请求 |
| 凭据 | 静态 Key、默认 Bearer、none/header/query 路由认证；支持 `checkKey`、`exchangeKey`、`credentialRejected`、已保存插件账号余额查询，以及持久登录/续期/撤销/定时维护，详见 `../../backend/internal/newapi/PROVIDER_CREDENTIALS.md` |
| next 请求链 | 最多 32 个后续请求，显式 state；正文默认为 POST，无正文默认为 GET。每步重新认证/签名，首步的业务头/prepare 不传递；允许 JSON 握手后接最终 SSE |
| 预置/组合 | 固定 RelayKit 的 Chat→Responses/Anthropic/Gemini、Responses→Chat/Gemini，支持 JSON/SSE 和 `<preset>+plugin` |
| 工具费用 | hook 的任意已定价 `tools` 增量、公开协议调用、Anthropic/xAI/阿里/Azure 累计报告与 Gemini grounding 去重；逐模型售价、显式零覆盖及历史售价/倍率明细，沿原 token 账本一次结算，详见 `PROVIDER_TOOL_PRICING.md` |
| 未开放 | 其他公开入口、multipart/文件/WS/AWS 二进制流、非 JSON 二进制 chunks、厂商私有工具用量的自动提取 |

### 工具用量与售价

`convertResponse` / `convertStreamEvent` 可返回 `tools: {工具名: 次数}`，这是**本次 hook
的增量**，不是累计值；同一请求的 next、最终响应和实际执行的 EOF 共用累计器。凭据拒绝和
错误不增加事实；单工具最多 10000 次，显式 `0` 覆盖同 key 的协议推断，其他 key 保留。

插件中心的 Provider 工具售价表使用 `工具名:模型前缀*` 和最长前缀优先规则，单位统一为
USD/千次，必须显式配置。没有命中时，两种 web_search key 可回退到旧分组的明确搜索价；
不导入厂商默认价格，不把缺价当免费。请求开始时固定价格表、计费模型和回退价，在途不改价。
标准 Responses 实际 hosted 调用及已定价的自定义函数会被计数，过程事件与最终快照去重；
工具声明不收费，同名客户端函数不冒充 hosted 工具。token 与工具经过原分组倍率和唯一账本
一次结算。准确范围、错误和配置方式见 [Provider 工具计费](PROVIDER_TOOL_PRICING.md)。

### Embeddings

`relay` 使用 `incoming_path: "/v1/embeddings"`，转换器可为 `none` 或 `plugin`。
复用相同固定源码、连接/登录凭据、路由认证、签名、next 与单次凭据续期；不经过原生账号直通兜底。
客户端模型别名、实际上游模型、原分组价格和现有用量账本仍分开保留。
返回必须为标准 JSON `data[].embedding` 和明确的非负整数输入用量；有效零用量可以接受，
缺失或非法用量不能变成成功零费用。最终响应和转换各最多 1 MiB；Embeddings 不接受流式或生成参数后缀。
Embeddings 已接 Transform 请求阶段与 HTTP report（`openai` 格式）；沿固定基线跳过文本 reply，不接受 reasoning.set，也不附加生成策略或流式选项。其他媒体/WS 传输仍未开放。

内置本站示例 `sub2api-openai-relay` 的 `1.1.0` 声明了此入口。新工厂版本登记不迁移既有账号，
需要管理员明确切换固定 revision；旧 `1.0.0` 任务/账号仍保留原约定。

请求准备不能改变 `model`、`stream`、`stream_options`、`n`、`generationConfig.candidateCount`、`size`、`quality`、`resolution`、`seconds`、`duration`、`service_tier`，包括新增或改写其大小写别名。转换可以改变协议字段的表示，但准备阶段不能再偷偷改变这些计费字段。认证、Cookie、Host、Content-Length/Type/Encoding、hop-by-hop 和路由指定的认证头受到保护。任何目标覆盖仍受插件允许域名、服务端 URL 策略、公网 DNS 和禁止重定向的限制。

配置校验和请求转换/准备各最多 5 秒；静态凭据交换最多 15 秒，单次初始化或换令牌重建总预算最多 25 秒。每个响应钩子含 worker 排队最多 5 秒。请求准备/转换、JSON 响应，以及流输入事件、输出批次和 state 的上限各为 1 MiB。最多调用 65536 次流钩子；等待上游流数据超过 30 秒中止。worker 每次执行后释放，不等待整个视频/文本生成结束，也不等待慢客户端消费。客户端取消会关闭上游。

宿主不会自动补 `[DONE]`。插件必须根据真实完成信号输出当前客户端协议的结束事件；只有读取到干净 EOF 才收到 `done: true`，传输失败不调用完成钩子。标准协议处理器也可能在收到终态后结束读取，因此不要依赖 done 回调执行必须完成的清理。HTTP 200 错误正文、转换失败和缺少终态不能当作成功，HTTP 非 2xx 的终结正文不能通过转换伪装成成功；插件可以明确返回 next 处理可恢复错误，但 HTTP 401 或 credentialRejected 已确认的凭据拒绝不能授权 next/state/tools，必须由宿主的一次续期或原错误处理接管；错误转换自身失败时保留原 HTTP 错误。成功响应返回未支持的二进制或缺价工具费用会明确失败，不悄悄丢弃。

## 预置协议与组合插件

宿主直接使用固定基线的 RelayKit `v0.3.8-0.20261120084210-48f9df96ea7c`，下列 `converter` 可直接填写到 relay，或加 `+plugin`：

| 下游 → 上游 | converter |
| --- | --- |
| Chat → Responses | `openai_chat_completions_to_openai_responses` |
| Chat → Anthropic Messages | `openai_chat_completions_to_anthropic_messages` |
| Chat → Gemini | `openai_chat_completions_to_gemini_generate_content` |
| Responses → Chat | `openai_responses_to_openai_chat_completions` |
| Responses → Gemini | `openai_responses_to_gemini_generate_content` |

组合路径的顺序是：客户端 → RelayKit 预置请求 → `convertRequest` → `prepareRequest` → 路由认证/签名 → 上游；回来时先经插件 `convertResponse` / `convertStreamEvent` 还原预置的上游协议，再由 RelayKit 转回客户端格式。因此 `ctx.path` 仍是原公开入口，但传给插件的 `req.body` 已是预置目标协议，不要重复转换客户端正文。后续 next 仍共享本次转换上下文和工具映射，仅最终结果转换和计量；管理模型/余额查询不运行这些预置。

所有预置支持普通 JSON 和 SSE。Anthropic 目标使用 `Anthropic-Version: 2023-06-01`，需要客户端显式提供 `max_tokens` / `max_completion_tokens`；本站不猜测厂商输出上限。Gemini SSE 将 `:generateContent` 改为 `:streamGenerateContent` 并添加 `alt=sse`，认证和签名在最终目标确定后执行。公开模型、上游模型和最终账单身份沿原网关保留。函数工具和 Responses 自定义工具的往返映射复用 RelayKit 的请求状态；已接工具 hook 用量，厂商私有费用仍需插件报告，不能认为 codec 会自动提取所有工具账单。

预置转换额外限制单份 JSON 1 MiB、8192 个节点、64 层，流累计输入 JSON 最多 1 MiB，防止小对象或长状态耗尽宿主内存。只有真实 finish_reason / Responses 终态 / Anthropic message_stop / Gemini finishReason 后的干净 EOF 才允许成功收尾；错误、断流和无终态不会自动补成功。协议独有字段的映射质量沿固定版 RelayKit，不保证各厂商所有字段无损；需要外部素材解析的转换仍需尚未开放的媒体宿主，不能因安装 codec 就视为已支持。

## 流式上游返回普通 JSON

下游的 `stream:false` 可以在 `convertRequest` 或后续 `next` 中明确返回 `stream:true`，让宿主按上游流读取。插件在 `convertStreamEvent` 中处理厂商事件，通过 `state` 传递进度，并返回以下一种结果：

- `body`：完整标准 Chat/Responses JSON。该结果表示转换完成，宿主立即关闭上游，不再等 EOF；需含真实终态及实际用量。
- `chunks:[{bodyBase64:"…"}]`：按次序拼成一份标准 JSON，第一次 chunks 结果必须声明 JSON `contentType`；直到干净 EOF 才交付。字节可在 UTF-8/JSON 标记中间分段，最终整体仍须合法。不能再返回 body。

宿主只执行插件的明确输出，不从厂商 delta 猜测最终正文。JSON 客户端不能返回 SSE events，SSE 客户端不能返回 body/chunks；`send/close` 仍需尚未开放的 WebSocket 宿主。流结束仍无正文、断流、超过累计 1 MiB、非法 UTF-8、重复 JSON 键或非法标准协议都返回失败，不计作成功；处理中可暂不输出并继续传递 state。

整个 JSON 聚合沿用原链 2 分钟总预算、30 秒上游读取空闲限制、65536 次钩子和每次 5 秒预算；JS worker 在等待网络时归还，客户端取消关闭真实 body、归还原共享内存预留，没有新增后台队列。SSE 客户端的健康长连接仍沿用原时限。

凭据拒绝仍全链最多续期一次、只重试当前步骤。若 POST/PUT/PATCH/DELETE 已收到上游事件，之后才报告凭据错误，则可能已经开始计费生成，宿主仅返回失败并失效令牌，**不会自动重放该变更请求**。GET/HEAD 查询可按原约定续期重试。`nativeResponse` 不承担流转 JSON 聚合，必须走明确转换。

进入 `next` 后，任何后续 HTTP/传输失败都不允许网关重新换账号或按账号池重发整条链；上游流已受理后的晚到凭据错误也遵守此规则。Chat、Responses、Embeddings 共用该保护，首步尚未进入后续步骤的明确拒绝维持原故障切换规则。已有实测部分结果仍沿原用量入口结算；没有最终结果时不补造零 Token 或工具账单。这是当前请求的自动重试保护，不是跨请求持久幂等，也不代表首次网络提交未知的恢复已完成。

## 静态 Key 与令牌

可选凭据钩子使用 New API 的原有签名：

```js
// 保存新连接或修改 Key、配置、地址、插件版本或代理时执行。
export function checkKey(ctx, key) {
  const result = utils.fetch({url: ctx.baseUrl + "/auth/check", headers: {"X-API-Key": key}});
  if (result.status !== 200) return {error: {en: "Key rejected", zh: "Key 无效"}};
  return null;
}

// 仅需要换取短期令牌的上游声明此函数。utils.fetch 的 JSON body 已解析。
export function exchangeKey(ctx) {
  const result = utils.fetch({url: ctx.baseUrl + "/auth/token", method: "POST",
    headers: {"Content-Type": "application/json"}, body: JSON.stringify({key: ctx.apiKey})});
  if (result.status !== 200) return {error: {status: result.status >= 400 ? result.status : 502, message: "Token exchange failed"}};
  return {token: result.body.access_token, expiresAt: result.body.expires_at};
}

export function signRequest(ctx, request) {
  return {headers: {Authorization: "Bearer " + ctx.token}};
}

// HTTP 401 由宿主直接视为凭据拒绝；其他非 2xx 可由插件明确识别。
export function credentialRejected(ctx, response) {
  if (response.status !== 419) return false;
  try { return JSON.parse(response.body).code === "token_expired"; }
  catch { return false; }
}
```

这里的鉴权 URL、字段和状态码是开发示例。`checkKey` 只能 GET/HEAD，`exchangeKey` 可 POST；两者最多 4 次受限 fetch、每个响应最多 1 MiB，并沿用账号代理、允许域名、公网 DNS 和禁重定向策略。配置中的 URL 仅在 schema 声明相应 URL 字段时扩充允许主机。其他转换、准备、签名和拒绝识别钩子不能 fetch。

校验失败阻止保存；为了避免脚本或厂商错误回显密钥，界面只显示通用错误，不透传原始异常或拒绝文案。连接身份未变的编辑跳过远程 Key 校验，插件配置本身仍会校验。

交换结果的 `token` 为不超过 64 KiB 的 JSON；`expiresAt` 是 Unix 秒，0 或省略表示无声明的过期时间。已经过期的结果拒绝；有效期不足 30 秒的令牌只供本轮使用，不进入缓存。`convertRequest/prepareRequest/signRequest/convertResponse/convertStreamEvent/credentialRejected` 读取同一轮的 `ctx.token`。

缓存仅在当前进程内共享，最多 10000 项、128 个不同连接的在途交换；按账号、固定版本/源码、Key、地址、配置和代理隔离。同身份的并发请求合并为一次交换；取消一个请求不会取消其他请求等待的交换，该后台交换最多持续 15 秒。失败不缓存，令牌不落库、不回传页面，节点重启后按需重取。

上游 HTTP 401 或插件对其他非 2xx 明确返回 `true` 时，只清除本轮实际使用且仍匹配的缓存令牌。具有 `exchangeKey` 时，在当前请求固定的源码、连接和配置下重建、重新签名并重试一次；仍失败则进入既有账号错误处置。网络失败、成功流以及未被明确识别为凭据错误的 5xx 不触发这次续期重放。最终失败不产生成功用量，最终成功仍只进入一次现有计费链。没有 `exchangeKey` 的静态 Key 不自动重发。

声明 `login/refresh` 的 Provider 仍不会开放普通转发；目前仍未接入 OAuth 生命周期及定时维护；静态 Key 余额查询见下节。原生“测试连接”和“同步模型”不会绕开插件，实际业务验证仍使用本站带分组的 API Key。

## 本阶段验证

真实 JS 子进程与模拟上游覆盖两种标准入口的 JSON/SSE/NDJSON、模型映射后的精确字节签名、prepare 顺序、认证保护、非法目标、计费字段、固定版本和配置、跨事件 state、慢客户端不占 worker、断流/错误/取消及既有账本。未使用真实厂商 Key，没有发起付费模型调用；生产上游仍需按实际协议联调。

静态凭据阶段另外覆盖：无过期/临期/过期令牌、缓存容量与隔离、并发合并与调用方取消、真实 worker 并发构建、所有转换阶段的固定 token、401/自定义拒绝的一次续期、迟到拒绝、重试固定连接、交换失败不缓存与敏感信息处理、账号异常策略、普通编辑跳过检查及代理单独修改后的校验。New API 契约全量、相关 service/handler/repository 聚焦回归、service/handler 竞态检测、前端类型/ESLint、5 文件 15 项前端测试和前后端生产构建均通过。此阶段没有数据库结构变更，也没有推送、发布或部署。

## 有界 next 请求链

`convertResponse` 可以返回 `{next: {path, body?, headers?, method?, stream?, delayMs?}, state}`。每步响应只决定下一步或最终标准响应，旧 state 不会自动合并；无 state 时下一步读取 null。当前支持 JSON 正文和无正文的后续请求，`delayMs` 最多 10000，等待可被取消。下一步可接管 HTTP 非 2xx，但最终非 2xx 仍进入原错误处理，不伪造付费用量。

源码、配置、基址、Key、账号与客户端协议在首步固定。`prepareRequest` 和请求处理规则仅在首步执行，后续步骤不继承前一步的业务头或签名，只采用当前 step 自己的头，再写入原路由认证并对实际字节重新签名。发生 HTTP 401、`credentialRejected` 或转换后的 401 时，全链仅允许一次令牌续期；在 poll 阶段过期只重发该 poll，绝不重放已接受的生成请求。被拒绝步骤返回的 tools、next 和 state 不生效，没有续期能力时也不会继续执行其 next。中途修改账号或停用插件只影响新请求。

32 次后续请求之外另有最多一次凭据重试；前缀执行总预算 2 分钟（HTTP、JSON 读取、所有 hooks、延迟和续期计入）。选择最终 SSE 后移除这段总时限，继续使用原流的空闲/读写/取消限制。JSON 握手可以用 `stream:false`，最终 step 设置 `stream:true` 或返回 SSE/NDJSON。JSON 客户端也可按前节契约由插件明确返回完整 body 或 JSON chunks 聚合；仍受累计 1 MiB 和两分钟预算约束，不猜测厂商 delta，也不接受二进制 chunks。

每个 Provider 尝试在任何转换前预留共享 16 MiB 临时内存，结束或取消释放；容量不足返回 503/Retry-After，正文超 1 MiB 返回 413。每步 body/state/JSON 响应各最多 1 MiB，不累积保存所有响应。HTTP 正文在离开当前步骤前关闭，取消也会关闭实际网络正文。凭据交换与步骤构建复用原 worker 和缓存，没有新增后台队列。最终标准响应用量及链中已确认的工具增量进入现有账单和完成报告，中间查询不独立扣费；未配置的正数工具用量明确报错，不能静默算零费用。

## 手动查询上游余额

在“编辑账号”的插件区域，可对已保存且同时提供 `buildBalanceRequest/parseBalance` 的固定版本点击“查询上游余额”。修改未保存的地址、Key、配置不会参与查询。可显示金额、币种、无限额及额度窗口；没有金额且无限额为 false 时，插件必须返回至少一个有效窗口，不能把空结果当成 0。

查询复用账号并发、代理、固定源码及已验证配置，使用插件返回的管理鉴权和签名，不继承 relay 路由鉴权；可经最多 32 个后续 JSON 请求，401 全链最多交换一次令牌并仅重试当前步骤。每正文/state/响应最多 1 MiB，单次 delay 最多 10 秒，全查询最多 45 秒；内存先准入，网络等待不占 JS worker，取消关闭真实响应并释放并发与预留。

这是上游在查询时刻的观察值，不更新本站余额、售价、账单、健康或路由。完整管理员权限、二次验证与审计后才能调用 `POST /api/v1/admin/newapi-plugins/accounts/:id/balance`，默认不查询也不轮询，不持久保存结果。历史 revision 元信息通过同权限的只读 `/versions/:id/info` 获取，不切换账号默认版本、不执行或返回源码。OAuth 凭据和自动定时查询仍未开放。

## 插件模型发现

当固定插件版本显式声明 `incoming_path: "/v1/models"` 时，编辑已保存账号可点击“查询上游模型”；这是插件的管理接口，不能退回平台原生 `/models`。支持 `none` 和 `plugin` 转换器，管理路由只需要 `convertResponse`，不会调用生成用的 convertRequest、prepareRequest 或请求处理实例。路由地址可使用已验证的配置 URL，保留账号基址路径；默认 GET，next 有 body 时默认 POST，逐步按路由鉴权并签名。

`convertResponse` 返回标准 `{body:{object:"list",data:[{id:"..."}]}}`，或通过 next/state 整理分页。宿主允许最多 32 个后续请求，HTTP 401 或转换出的 401 全链只续期一次，仅重发当前页；错误状态不能当作成功空列表。原生响应的空 data 数组是有效空列表。管理阶段不接流、文件或额外工具计费；可使用已保存静态 Key 或已登录的 Provider 插件凭据，不接管 Sub 原生 OAuth。

管理 POST `/api/v1/admin/newapi-plugins/accounts/:id/models` 沿完整管理员权限、二次验证及审计；45 秒总预算、正文/state/响应各 1 MiB、最多 4096 个不同模型 ID；拒绝歧义 JSON、控制字符和通配符。共享 16 MiB 先准入，复用账号并发、代理、四 worker、固定配置和令牌缓存，取消关闭真实响应并释放。结果仅显示模型 ID 与查询时间，不透传厂商元信息或敏感原始响应。结果不会自动修改账号映射、能力声明、售价或下游模型可见性，管理员仍需明确配置分组与公开映射。
