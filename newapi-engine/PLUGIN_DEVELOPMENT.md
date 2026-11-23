# 插件开发规范

渠道插件负责“把标准请求翻译成上游请求，再把上游结果翻译回来”。账号、权限、并发、任务、文件、钱包和日志由宿主负责。先完成离线回放，再用有额度的测试账号验证实际渠道。

日常独立开发社区/自定义插件请直接在同级独立项目 `../code1024-plugins/` 中进行（内置标准文档、开发目录、`Makefile`、`tools/plugindev` 与外部市场 `index.json` 生成）。本文件保留 `newapi-engine` 引擎侧的基础规范与底层 `pluginctl` 命令说明。

本引擎固定参考 New API `48f9df96ea7c68eae04a0f3687869f232f48929f` 的 API v1。原版 JS 可直接上传，但只有兼容矩阵标明已接通的宿主功能可以运行；不能因为编译成功就宣称所有 New API 插件都兼容。准确范围见仓库根 `backend/internal/newapi/HOST_COMPATIBILITY.md`。

## 1. 创建项目

在本目录构建独立工具：

```sh
GOWORK=off go build -o /tmp/pluginctl ./cmd/pluginctl
/tmp/pluginctl init task /tmp/my-video-plugin --key community-1-example
cd /tmp/my-video-plugin
/tmp/pluginctl lint plugin.js
/tmp/pluginctl test plugin.js --fixture fixture.json
```

`init` 支持 `task`、`provider`、`ingress`，生成 `plugin.js`、`fixture.json`、`README.md`。目标目录必须不存在，父目录必须已存在；不覆盖旧文件。上述 key 只是示例：向本站社区投稿时，使用“插件开发”页面显示的当前用户命名空间。key 限 1～30 个小写字母、数字、`_`、`-`，首位须为字母或数字。

模板上游为虚构地址，fixture Key 也是假数据。请根据真实厂商文档改字段、状态和实际用量；模板通过测试不表示能直接调用任何渠道。CLI 和网页试运行共用同一版模板/示例。

## 2. 插件身份与配置

- 单个 UTF-8 `plugin.js`，导出 `meta` 与对应 hooks。`meta.apiVersion` 为 1，key 稳定、`meta.version` 为语义化版本。改源码后升级版本；已发布源码、SHA256 和任务引用不可原地改写。
- `meta.models` 填真实支持的具体模型；动态 Provider 需结合宿主账号的显式模型映射。通配声明不表示当前分组有权使用所有模型。
- 用 `channelConfig` 声明每个账号的连接配置，用 `configSchema` 声明请求处理实例配置。只用基线支持的 schema 类型和 URL 格式；实际秘密由账号和密文宿主提供，不放进源码或公开元信息。
- 仅声明实际实现的协议、supports 和 required capabilities。未知或未实现能力在安装/启用时明确报告，不能悄悄转成别的协议。
- 纯 JS 导出/工具按固定基线编写；不要引入 Node 的 fs、进程、环境变量或包安装逻辑。当前发行物就是 JS 源码，不是原生 `.s2plugin` 包。

## 3. 三类插件

| 类型 | 当前主要入口 | 作者的责任 |
| --- | --- | --- |
| Task | 通用 `/v1/tasks/:key`、OpenAI Videos/Images/Responses 和声明的 native 路由 | 解码、提交/受理、状态查询、最终用量、产物和下载描述符 |
| Provider | Chat/Responses/Messages、Embeddings、Images 与非流式 Audio 的兼容子集 | 声明 relay 路由、转换请求/响应、准备/签名和准确用量 |
| Transform | 兼容矩阵中的入口、HTTP 六阶段与 Task 检查/报告接点 | 返回相应阶段支持的 effects；不越权修改路由、模型身份、认证或账务字段 |

一份 JS 可以声明多类能力，但每项都需要宿主支持。安装前查 [宿主兼容矩阵](../backend/internal/newapi/HOST_COMPATIBILITY.md)，确认使用的入口、协议、文件和价格在兼容范围内。

### Task 必备约定

- 用 `protocols.openai_video.decodeRequest` 解析支持的输入，给出 `kind`、公开模型、action 和 requestBody；不认识的规格应在付费提交前拒绝。
- `buildSubmitRequest` 只返回描述符；宿主校验并发送。`parseSubmitResponse` 必须返回真实 taskId，或者契约允许的明确 immediate result；HTTP 2xx 没有受理证据不算成功。
- `buildQueryRequest` 查询原上游任务。`parseTaskResult` 只映射有证据的 `QUEUED`、`IN_PROGRESS`、`SUCCESS`、`FAILURE`；未知返回 `UNKNOWN`，禁止 `|| "IN_PROGRESS"` 无限假运行。
- `extractUsage` 只提供预估；`extractUsageOnComplete` 提供真实最终数字。缺少最终秒数/Token/费用时返回缺失，不回填创建估计。枚举/布尔规格需与 `usageSchema/usageProfiles` 一致。
- `listArtifacts` 声明实际产物，`buildContentRequest` 构造受保护交付；公开响应使用本站 task ID/下载入口，不把上游账号、任务 ID、签名 URL、Key 或 state 当作公共字段返回。
- 派生任务引用必须经宿主归属校验，固定原账号、版本、连接和登录身份。提交超时结果未知时，不在脚本里重发付费 POST。
- `meta.protocols` 声明 `openai_responses` 的 `sync`/`background`/`stream` 模式时，按声明实现 `decodeRequest`、`renderFinal` 和/或 `renderEvents`。依靠 `ctx.artifacts` 返回本站产物链接。图片编辑的 `openai_image.decodeRequest` 会收到 `operation: "edit"` 和 multipart 文本/文件引用。
- `meta.routes` 的路径挂在 `/v1/tasks/<插件 key>/native` 下。例如声明 `/suno/fetch/:task_id`，调用 `/v1/tasks/sunoapi/native/suno/fetch/<本站任务 ID>`；查询不使用上游 ID。原生响应 wrapper 只允许受限状态、`Location` 和 `Retry-After`。
- 可选 `buildCancelRequest` 生成取消请求；必须由 `parseCancelResponse` 明确确认已取消，且上游 HTTP 成功，宿主才确认退款。没有解析器或确认时，仅代表取消请求已发出，原任务继续查询。

详细请求/文件/原生入口示例见仓库根 `docs/customization/VIDEO_PLUGINS.md` 和 `backend/internal/newapi/{FILE_HOST,DERIVED_TASKS,NATIVE_VIDEO_INGRESS}.md`。

### Provider 与 Transform

Provider 遵守共享 RelayKit 转换和现有路由契约，不把 HTTP 错误或断流补成计费成功，不编造 usage；不支持的 stream/媒体/工具费用必须明确拒绝。当前宿主支持通用 `tools` 的逐 hook 增量和同 key 显式零覆盖，使用管理员配置的逐模型 USD/千次工具售价；缺价不默认免费。Anthropic 预置的 `server_tool_use`、OpenAI 兼容 wire 的 xAI `server_side_tool_usage_details` / 阿里 `x_tools` / Azure `tool_usage` 与 Gemini 预置的 grounding 查询去重已在转换前观察，hook 明确计数优先；其他私有厂商用量须由插件准确报告。Gemini 记录每查询与每 grounded prompt 两种指标，不收费的指标也要明确配零。模板为非流式示例。详见仓库根 `docs/customization/PROVIDER_PLUGINS.md` 和 `docs/customization/PROVIDER_TOOL_PRICING.md`。

Transform 只返回其阶段允许的补丁/检查结果。`request/outbound` 按基线不提供 fetch；其他允许联网的阶段仍需宿主显式授权，默认关闭。附件是有界引用和文本投影，不是任意文件系统访问。详见仓库根 `docs/customization/REQUEST_PROCESSING.md` 与 `backend/internal/newapi/TRANSFORM_MEDIA.md`。

Provider multipart 的 `convertRequest` 从 `req.form.fields` 读取文本字段，从 `req.form.files` 读取文件元信息；一个值为字符串，重复值为数组。返回 `form`、`file` 或带 `$file` 的 JSON 时由宿主装配真实字节并签名，JS 不直接接收附件字节。Audio 账号须为模型明确配置按次售价；未配置时不向上游发送。

## 4. 计价与任务事实

插件只能报告实际用量/上游费用，不能修改用户余额。售价在宿主账号/分组设置，支持固定价、人工上游成本加价、最终账单和受限 Task 表达式。`cost_plus` 可以选择接入 `quotePrice` 或成对的 `buildQuoteRequest`、`parseQuoteResponse`，报价和最终账单分开记录。

Task 表达式支持字面量时区的 `hour/minute/weekday/month/day("时区")`，空或无效时区按 UTC，周日为 0。宿主固定受理时刻和当时的 UTC 偏移，重启和终态必须复用原快照；不得用脚本当前时间代替原定价时间。完整规则与受限语法见 `backend/internal/newapi/TASK_PRICING.md`。

费用应包含实际币种、计量单位、来源和 final 证据，不能把估价写成最终账单。宿主在受理时固定价格依据并冻结上限，在终态后按实测用量结算或退款；无最终用量/超上限则待核对。成片已经可下载和账单尚未确认可以同时存在。查询日志不应触发生成或再扣一次费。

## 5. 离线回放与测试要求

`fixture.json` 沿用固定 New API 的 `{ unixNow, cases }` 契约。每个 case 提供 `hook`、可选 `path/member`、JSON `args`，以及完整 `expected` 或 `expectedError`。时间相关逻辑使用 `utils.unixNow()`，以便用 fixture 的时间重放。

提交前至少覆盖：正常输入、非法输入、受理号缺失、处理中、成功、失败、未知状态、最终用量缺失、实际零用量、授权/签名错误和支持的文件/派生操作。不支持的能力应有拒绝用例。网页离线试运行使用全新有界子进程、禁网且无真实账号/钱包；CLI 回放也不提供网络/账号宿主，不能用回放代替真实 I/O 或事务验收。

开发宿主或 Task adapter 时还要运行：

```sh
cd newapi-engine
GOWORK=off go test -race ./...
cd ..
# 在仓库根运行隔离 PostgreSQL / HTTP / 资金验收
GO_BIN=/path/to/go PG_BINDIR=/path/to/postgresql/bin ./backend/scripts/test-video-plugins.sh
```

多实例租约、未知付费提交、重启恢复、资金/额度 outbox 和权限必须由宿主集成测试验证。宿主接入规范见 [HOST_ADAPTER.md](HOST_ADAPTER.md)。

## 6. 投稿、市场与升级

1. 在“插件开发”保存命名空间内的草稿，运行模拟输入，确认兼容报告。
2. 提交管理员审核；发布后的版本不可修改，升级另发版本。撤回不应删除既有任务依赖的源码。
3. 本站社区市场由本站审核，外部 New API 官方/社区/第三方来源是另一组目录；本地投稿不等于已发布到外部官方市场。
4. 安装固定 key/version/SHA256，安装前重查目录和撤回列表；安装不隐式启用。系统 key 被覆盖需要管理员明确确认。
5. 启用后在“账号”绑定版本/配置/分组/模型/价格。已有账号和在途任务不自动跟随新默认版本，先小额度验证后显式升级。

脚本来源遵守 AGPL-3.0-or-later 及相应通知；保留源码和固定基线记录。当前工具与 UI 包尚未独立发布版本，推送、Release 和生产部署分别验收，不能把本地测试结果当作上线完成。
