# 请求处理插件：入口、HTTP 请求与回复、Task 报告

本阶段把 New API 的入口处理钩子接到现有网关。可以把它看作模型调用前的一道检查：先运行全局规则，再运行当前分组规则，通过后才进入模型许可、选渠道、现有审核与计费。

兼容基线：`48f9df96ea7c68eae04a0f3687869f232f48929f`。完整能力矩阵见 `backend/internal/newapi/HOST_COMPATIBILITY.md`。本文描述已实现的 JSON ingress、全局/分组及原生或 Provider 账号的 HTTP 请求阶段、全局/分组及原生或 Provider 账号的 HTTP 回复检查、Task JSON egress 与完成报告 report，不承诺全部 New API 插件能力。

## 管理流程

1. 在“插件管理”安装带 `ingress`、`egress` 或 `report` 的 JS 插件，或使用已经自动登记的内置版本。
2. 打开“请求处理”，新建实例，填写名称，选择固定源码版本、全局/指定分组或受支持的已有账号、请求格式范围、处理阶段、精确模型名与排序。新实例默认停用、未选分组；模型留空才表示该作用域全部模型，不接受通配符。
3. 修改实例 JSON 配置，使用示例请求试运行。试运行只测试这个版本和配置，不模拟多个实例的排序，不向模型发请求。
4. 试运行通过后保存并启用。启停、保存、试运行与读取完整配置沿用现有管理员二次验证。
5. 在“执行记录”按请求 ID 查阶段、动作、固定版本、耗时、效果数和命中数。

Task/Provider 插件的总开关不控制这些实例。相同源码可以建立多个不同配置的实例。实例固定源码 revision，更换版本需要新建实例，试运行后切换启停；修改插件默认版本不会改变已保存实例。

同一作用域按排序值、实例 ID 排序，全局总在分组前。一个 HTTP 请求只执行一次入口阶段，账号重试不会重复执行。单请求固定配置快照；其他请求最迟约 2 秒读取新配置。并发请求共享一次有 2 秒截止时间的配置刷新，客户端取消可以立即停止等待；缓存过期刷新失败会返回 503，不继续使用过期的安全规则。

## 当前接入范围

- JSON `POST`：Chat Completions、Embeddings、Images generations/edits（含根路径别名）、Responses（含根路径和 Codex 别名）、Messages（含 Antigravity）、Gemini `generateContent` / `streamGenerateContent`、`/v1/videos`。
- 全局和指定分组作用域，可叠加请求格式及精确模型范围；插件自己的 `meta.scope.models` 同样参与匹配。插件的 `meta.scope.channelTypes` 按实际账号匹配；带此声明的 ingress 需明确选 Chat/Responses/Claude 或 Task 格式。
- 只接受 `json.patch`。Patch 全部成功才应用；失败不保留这个实例的部分修改。
- 匹配到规则时，请求体及处理后正文最多 1 MiB，实例配置最多 64 KiB；不支持的非 JSON 正文返回 415，过大正文返回 413。没有匹配实例的请求沿用原有链路限制。
- 保护 `model`、`stream` 以及 session 中对应字段，包括大小写变体和重复键。不能靠入口插件扩大可选模型或更改流式协议。
- 入口总预算 5 秒，包含 worker 排队。必选插件失败返回 503；显式声明 `meta.optional` 的插件失败可跳过并记录。客户端取消不会继续执行。

全局/分组及当前原生或 Provider 账号已支持 Chat/Responses HTTP 请求、回复和报告六阶段组合；尚未接通的实际原生协议（如 OpenAI 账号经 Anthropic 出站）带尝试阶段规则时会在生成前拒绝，不静默跳过。Messages/Gemini 的原生回复与报告现已接入，下文列出边界；标准视频客户端 multipart 附件已接早/晚 ingress；WebSocket、其他持久文件及其他 multipart 接点尚未接入；JSON 内嵌附件已支持有界文本/PDF/DOCX 读取，同请求多个实例和阶段共享提取预算，效果与边界见 `backend/internal/newapi/TRANSFORM_MEDIA.md`；Task egress 见后文。已接通的 ingress/egress 可显式开启下述受限联网，reply 使用独立授权。普通 Provider 插件自身的转发与凭据维护不属于此开关。子进程隔离沿用现有运行时，不等于操作系统沙箱。

### 全局 / 分组与原生账号的回复检查和报告

HTTP 实例可直接选择使用 OpenAI 网关的已有原生账号（包括受支持的 API Key、OAuth / setup-token）或 Provider 账号。无需为了回复检查或报告把原生账号改为插件账号。必须明确选择 Chat / Responses 格式，且只对最终实际选中的账号报告；全局、分组、账号报告保持此顺序，不包含 WebSocket。

原生账号的回复检查接在原网关转换后的实际 Chat/Responses JSON/SSE 上，支持原生 Chat、Responses 及 Chat 经 Responses 转换；不替换原协议转换器。全局→分组→当前账号，每次真实账号尝试单独准入和审计；已测量用量保持原账务，明确拦截取消真实上游，不伪造完成帧。全部作用域合计最多 16 个实例，共用原 64 MiB 内存预留、1 MiB JSON、4 MiB 流事件队列与累计 10 秒 hooks 预算；容量不足/初始审计失败在生成前拒绝，结束审计失败保留 started 记录并增加可见失败计数。账号、writer 和请求上下文在返回前恢复，不把 Gin 放到后台。

原生账号也可组合 ingress/request/outbound/egress。账号是否绑定 Provider 决定实际网关路径，不能改变匹配规则的启用状态；解绑后仍执行原生已支持路径的规则，未接通协议先拒绝。账号搜索先取最多 20 条，再按所选阶段/格式过滤；claude 格式的请求阶段可选原生 Anthropic/Gemini/Antigravity 账号，gemini 格式可选 Gemini/Antigravity 账号；阶段变化会取消旧搜索并刷新，迟到结果不能覆盖当前选择。

### Messages / Gemini 原生回复检查

`claude`、`gemini` 格式支持全局、分组及指定原生 Anthropic / Gemini / Antigravity 账号的
`reply`、`report` 或两者组合。支持 Anthropic Messages、Gemini 原生 generateContent /
streamGenerateContent、Gemini→Messages 和 Antigravity 的两种客户端协议。过滤器放在
原网关转换后的实际下发 JSON/SSE 上，继续使用原版 UTF-16 遮盖、暂存与停止规则；
思考块、工具数据、向量和图片内容不会被冒充为普通回答。Gemini countTokens 不执行回复过滤。

与 OpenAI 回复检查共用实例数、内存、执行预算、单独联网授权和开始/结束审计；不新增
后台队列或第二套账本。停止会取消真实 HTTP 读取，仍返回网关已经采集到的部分用量供原账本处理；
没有收到的最终 Token 不估算补齐，也不重发生成。普通返回会恢复原 writer / 请求上下文，
后续计费父上下文不会被过滤器关闭。模型回退时，钩子的上游模型身份随真实选择更新。
完成报告捕获脱敏后实际输出，并继续等待同一次原账本确认。

原生 Anthropic Messages、Gemini 和 Antigravity 的晚请求阶段已接入，详见下文。
全局/分组的规则命中尚未接通的路径时仍在发送前拒绝并审计，不静默跳过。
管理页可选择两种格式并离线试运行 JSON 回复；试运行不模拟真实账号、协议转换或 SSE。

### Anthropic Messages 请求阶段

`claude` 格式现支持全局、分组及指定原生 Anthropic 账号的六阶段组合，覆盖 API Key、
OAuth/setup-token、Vertex service account 和 Bedrock（API Key / SigV4）。`request` 读取客户端
正文与实际所选模型，`reasoning.set` 使用固定 RelayKit 渲染为该 Claude 模型的 thinking / effort /
采样设置，保留 output_config 中的格式 schema 及业务字段。未指定 override 时保留客户端明确控制。
这不代表这些入口的 `@` 模型参数后缀已经接通。

晚 ingress / outbound / 只读 egress 在原网关完成出站 JSON 构造后运行。它们不接收上游凭据，
不能改模型、流模式、speed、原生身份、认证头或厂商 envelope；业务头继续由宿主附加，账号头覆写最后生效。
Vertex / Bedrock 的 JSON 不含模型字段时，仍由宿主提供固定的实际上游模型身份。Bedrock 的正文和业务头
在 SigV4 签名前处理，签名与实际发送一致。

同请求固定规则和配置；早入口不重复，原网关内部重试每次从其原生正文重新执行晚阶段，不叠加上次补丁。
透传的晚 ingress / outbound 仍记 unchecked，egress 可拒绝。本地 web_search 模拟没有对应原生出站接点，
匹配尝试阶段时明确拒绝。必选阶段、审计或容量失败在发送前终止；optional 失败不提交半个补丁。
结果继续使用原用量和原账本，实际 reasoning 同步到结果元信息。管理页可保存及离线试运行；离线不读取账号、
不获取真实凭据、不发送上游 HTTP，也不产生费用。

### Gemini / Antigravity 请求阶段

Gemini 与 Antigravity 账号可绑定 `claude` 或 `gemini` 客户端格式的 HTTP 六阶段组合；
原生 Anthropic 的请求阶段仍限定 `claude`。Gemini API Key、OAuth（直连及 Code Assist 项目包装）、
Vertex service account/API Key 与 Antigravity OAuth 继续使用原网关构造认证、模型和项目身份。
`request` 检查客户端 JSON；晚 ingress / outbound / 只读 egress 检查真实上游 Gemini JSON，
因此 Messages 客户端的晚阶段 `ctx.format` 也是 `gemini`。匹配范围始终按客户端格式和公开模型，
`ctx.upstreamModel` 表示本次真正发送的模型；OAuth 原有的模型映射约定保持不变。

`reasoning.set` 使用固定基线 RelayKit 渲染，保留响应 schema、业务字段及 includeThoughts。
Messages→Gemini 的便携推理意图在真实目标模型上渲染；Gemini 模型回退时重新渲染已选意图。
未知模型不补造思考能力，也不抹掉其原生控制。实际计费等级仍只取最终正文的显式 thinkingLevel，
thinkingBudget 不被推断为计费等级；回退到预算模型时不能保留旧等级。

OAuth / Antigravity 包装中的 project/model/request 外壳不会传给 JS。认证、x-goog-* 原生头、模型、
流模式、缓存引用、图片规格、候选数及除思考/普通采样以外的 generationConfig 保持原准入值；
大小写歧义、同字段的两种别名、未知新增的规格字段也拒绝。业务内容补丁和业务头仍原子提交。
每次物理重试从网关的原正文重新执行晚阶段；必选策略、审计或容量失败立即退出，
不能被签名修复、智能重试或模型回退吞掉，也不能转成换账号继续发送。

Antigravity 上游透传账号保持 Claude wire：只读 request 的便携推理渲染沿原生 Claude，晚 ingress /
outbound 记 unchecked，egress 仍可拒绝。countTokens、模型参数后缀和 WebSocket 请求阶段本批未开放。
管理页可选择格式、现有账号及单阶段离线试运行，离线不模拟授权、协议转换、SSE 或真实计费。

### Embeddings 请求阶段与报告

`POST /v1/embeddings` 沿固定基线使用 `openai` 格式。原生和 Provider 账号都执行早期 ingress、request、账号晚 ingress、outbound、只读 egress 和最终 HTTP report；作用域、实际账号、固定版本、受限联网、原子 patch 与唯一账本沿用原有链路。已存在的 `openai` 规则会覆盖此入口；仅想处理聊天的脚本应使用 `ctx.path` 区分。

与固定版 New API 的 `replyEndpoint` 相同，向量不进入文本 reply 钩子；不会把向量转成回答或改写其数值。Embeddings 不接受 `reasoning.set`，请求阶段返回该 effect 会按必选/optional 的原有失败规则处理；egress 不附加文本生成策略或 stream_options。最终 report 等待原账本确认，不新增账本或估计 Token。单阶段管理试运行仍按所选 Chat/Responses 示例运行，不模拟完整 Embeddings 入口。

### 原生 Images 请求阶段与报告

`POST /v1/images/generations`、`/v1/images/edits` 及其根路径别名使用 `openai` 格式。
原生 API Key、OAuth/setup-token 的 JSON 请求接 request → 账号晚 ingress → outbound → 只读 egress；
全局/分组早期 ingress 仍在路由、模型权限、审核和计价之前运行。省略 model 时按网关已有
`gpt-image-2` 默认值匹配规则，不能用无关 `session.model` 隐藏实际模型。公开模型、渠道映射
和账号映射按图片网关的原顺序解析，不套聊天的不同映射优先级。

账号晚 ingress/outbound 发生在准入之后，因此只允许修改顶层 `prompt`、`user` 和业务头；
其余字段（包括 n/size/quality、大小写别名、素材、SVG/帧数和未来扩展参数）保持原值。
不在这些阶段重定价或重新选择能力；想修改规格应使用早期 ingress。认证/长度/类型头继续
受统一保护。Images 不接受 `reasoning.set`，egress 不附加文本生成策略或 stream_options。

JSON 与 SSE 图片结果均不执行文本 reply，也不解析/改写成片。HTTP report 复用原唯一账本，
等待已提交费用，提供现有计费张数和已记录的图片输出/缓存 Token；未知费用不当作零。
OAuth/setup-token 的晚阶段在原网关构造的实际出站 JSON 上运行，并先于换票和发送。
direct 图片请求使用 `openai`；内部 Responses 桥接使用 `openai_responses`，可修改的提示词
位于 `input[0].content[0].text`，另可设置顶层 `user` 与业务头。`ctx.path` 始终是原图片
入口，`ctx.model` 为公开模型，`ctx.upstreamModel` 为本次图片模型；桥接的正文 `model`
是网关选定的内部文本模型，不能把它当作图片计价身份。图片工具、规格、素材，以及桥接的
instructions/reasoning/store/stream 均保持固定；认证、账号、指纹、Accept 和 OpenAI-Beta
头受保护。每次真实尝试只运行一次晚阶段，direct 端点回退时从原已校验参数重新构造并重新
执行；规则拒绝或必选失败停止回退，不绕过策略。内部文本 reasoning 不写入图片计费等级。

multipart 在必需请求/出站阶段仍明确拒绝，没有相应规则的既有流量保持原处理方式。
本批不开放 Provider Images、异步图片 Task 或全套媒体文件 Transform。单阶段试运行仍是
既有文本示例，不模拟 OAuth 图片转换与付费调用。

### 按渠道类型匹配与晚入口

`meta.scope.channelTypes` 使用固定 New API 类型号，不直接使用本站平台字符串：OpenAI API Key 为 1、Claude 为 14、Gemini 为 24、Kimi 为 25、智谱为 26、MiniMax 为 35、DeepSeek 为 43、Grok 为 48、Codex OAuth/setup-token 为 57、JS Provider/Task 为 61；没有对应类型为 0，不会匹配任何带类型声明的规则。这里的类型不表示某个协议的钩子已接通。

没有选中账号时，带渠道类型范围的规则不会施加入口 JSON 限制、执行钩子或发送报告。HTTP 报告仍沿用原有请求级有界预留和响应捕获，但不匹配时不入后台队列。选中账号后按实际类型重匹配：request → 尚未执行的 ingress → outbound → egress → reply/report。早期执行过的入口实例不重复；渠道范围延后的入口在每次真实尝试运行，配置仍固定请求快照。切换 Provider/原生账号会重新匹配，不能复用上次渠道的 reply/report 目标。

持久视频 Task 使用 61。带渠道范围的 Task ingress 在任务解码、估价和费用冻结前检查客户端 JSON，可以修改时长等尚未定价字段，仍不能改变模型或流模式。处理后的参数决定估价、公开任务参数和提交，原客户端参数继续决定幂等身份；已接受同键重试先返回原任务。拒绝、必选失败或审计失败不冻结费用，也不发送生成请求。标准视频 multipart 的早/晚入口可使用 `utils.attachments()` 和 `utils.media()` 查看本次暂存文件的只读元信息，文件不进入插件正文。规则只可用 JSON Patch `replace` 替换既有文本字段，不能改模型、流模式、文件引用、附件或 JSON metadata；预上传 `file_references` 也能在确认归属后提供只读元信息，私有 ID 不进入规则正文和任务报告。上游 multipart egress 仍记 unchecked。Task egress/report 只快照匹配 61 的实例；停用/改范围后的报告仍沿用原有创建时实例 ID、执行时启用/配置约定。

单阶段离线试运行有意绕过范围筛选，只验当前版本与配置的钩子，不模拟真实账号选择。带类型范围的 ingress 必须明确选择已接通的 Chat/Responses/Claude/Gemini 或 Task 格式，不允许默认全部格式。

### ingress / egress 调用外部规则服务

实例的 `network_enabled` 默认 `false`，需在原有管理员二次验证保存流程中单独开启。允许插件把请求内容发送给审核、脱敏等服务；不会自动携带下游请求头、账号 Key、账号代理或插件 state。报告 report 保留其独立联网语义，不受此开关控制。request/outbound 按固定 New API 契约始终不提供 fetch；reply 不能借此开关联网，需另行授权。

- 主机白名单来自实际固定版本源码的 `meta.allowedHosts` 与已验证的实例 `configSchema` URL 字段；普通字符串不是任意 URL 授权。继续使用服务端 URL 策略、实际连接的公网 DNS 校验及禁自动重定向传输；仅支持有界文本 GET/HEAD/POST，不提供媒体流上传。
- 每钩子最多 4 次、每响应最多 1 MiB；单请求最多 15 秒，但完整 ingress 或每个 Provider/Task egress 阶段仍限 5 秒（含排队、JS、所有联网），先到者生效。超过限制、取消或容量不足仍按现有必选/可选和审计规则处理；明确拒绝始终拦截，不变成上游故障。容量不足返回 503 并提示重试。
- 真正允许联网前，另从共享 ingress 内存预算预留 16 MiB，用于一个受限响应、RPC 和解析临时副本；钩子结束、异常及取消归还。这是准入账面值，不是 RSS 限额；复用原四个子进程槽与 HTTP 客户端，没有新增后台队列或 goroutine。
- Provider 请求固定入口时开关及配置；Task 创建时把开关和原 egress 版本/配置加密快照一起持久化。升级前实例/快照均保持关闭，编辑开关不改变已经接受的任务。Task multipart 仍标为 unchecked，不把文件内容发送给规则服务。
- 所有离线试运行强制禁网，包括 `late_ingress` 和 Task egress，不能通过提交此字段、修改示例或服务配置绕过。试运行无法验收外部规则服务的真实返回，需要实际受控调用测试。

迁移 `273_request_plugin_stage_network.sql` 保存此开关并约束它只能用于含 ingress/egress 的实例；没有改动账单和账号凭据。

reply 使用独立的 `reply_network_enabled`（默认 false），仅含 reply 的实例可保存此授权；迁移 `275_request_plugin_reply_network.sql` 不会把旧 ingress/egress 的授权扩展到模型回复。允许主机、DNS/URL、无凭据/代理、每钩子 4 次/每响应 1 MiB 和额外 16 MiB 共享内存准入与上文相同。单次 reply 调用最多 5 秒，全局/分组/账号全部回复 hooks 累计 10 秒，网络等待计入其中；请求开始时固定授权和配置，撤销只影响新请求。所有离线试运行强制不联网，不能通过请求字段伪造授权。模型回复可能发送到实例服务，管理页单独提示并确认此权限。


## 全局 / 分组 / 当前账号的 HTTP 请求阶段

在“作用范围”选择全局、指定分组，或从原账号管理搜索使用 OpenAI 网关的原生/Provider 账号。每实例最多 100 个账号；模型范围仍按客户端公开模型精确匹配，格式必须明确选择 `openai`、`openai_responses` 或两者。不创建第二套上游连接。HTTP 实例支持六阶段组合，不能混入 Task 格式；`meta.scope.channelTypes` 同样参与每次实际账号的范围匹配。保存时阶段按 ingress、request、outbound、egress、reply、report 排列；实际普通尝试的晚 ingress 在 request 之后运行。

顺序为客户端全局/分组 ingress → 原有鉴权、路由和模型映射 → request → 账号晚 ingress → outbound → 宿主服务档位/流用量设置 → egress → Provider convertRequest/prepareRequest → 认证/签名 → HTTP → 响应转换 → reply。egress 看见的是宿主协议 JSON，**看不到**随后 Provider 转换出的厂商专用正文，与固定基线保持一致。现有服务档位和用量设置仍先参与原准入，并在出站之后再次落实。

原生路径由现有网关负责 Chat/Responses 转换：request 在转换前运行，晚 ingress/outbound/egress 接在最终实际出站 JSON 上，因此它们看到的格式可能与客户端不同。原生账号凭据、OAuth/setup-token 和身份头仍由原网关注入；业务头不能改写认证、组织/项目、ChatGPT/Codex、会话等身份头。账号 HeaderOverrides 继续最后应用。原生透传正文保持原字节，晚 ingress/outbound 记 unchecked，egress 只读检查。尚未接通的实际协议在 HTTP 发送前明确失败并审计。出站推理强度修改会同步到实际用量日志元信息。

- request：读取客户端 JSON、公开模型、映射后上游模型及账号/尝试号；提供消息数、工具数、字符数、图片标记和客户端推理强度摘要。只接受 `reasoning.set`，支持 none/minimal/low/medium/high/xhigh/max。默认保留客户端已有值，`override: true` 才覆盖。宿主按 Chat 的 `reasoning_effort` 或 Responses 的 `reasoning.effort` 写入；Provider Chat/Responses 已支持 `@` 模型参数及豁免，原生模型参数仍待接入。原版 `auto-reasoning` 已用实际宿主回归验证。
- outbound：原子接受 `json.patch` 与 `header.patch`。模型、stream、stream_options、n、generationConfig.candidateCount、size、quality、resolution、seconds、duration、service_tier 及大小写变体保持不变；重复 JSON 键拒绝。认证、传输头及路由自定义认证头禁止改写。排队业务头最多 64 个/64 KiB，只向后续规则提供它们，不提供客户端/账号头；Provider convert/prepare 的显式同名业务头在之后应用。
- egress：只读，可通过/拒绝/记录 findings，非空 effects 非法；可按上节显式开启受限 fetch。request/outbound 仍不提供 fetch。当前可用附件元信息/内嵌 UTF-8 文本读取，ingress/outbound 可用 media.text，HTTP outbound 可用原版 JPEG/PNG/WebP 图片重编码；大文件、PDF/DOCX 和其他协议仍不据此开放。
- inline data URL/带 MIME 的 Base64 字段只向 JS 投影为 `[media:N]` 和 MIME/大小描述；真实 JSON 保持原字节，出站补丁不能插入这些占位符（包括转义写法）。utils.attachments/media/mediaText 通过宿主有界回调访问；payload 不进入 JS。已授权 fetch 可由宿主流式展开本次 media ref，仍受原有目标与网络权限限制。
- 所有上游尝试复用入口时固定的实例、源码版本和配置；每次实际上游尝试重新运行所选的 request、晚 ingress、outbound、egress 阶段，包括 Provider 401/明确拒绝触发的那一次有界凭据更新。每次从原本次请求的正文构建，上一尝试的 patch/header 不串入下一尝试，已经在客户端入口执行过的全局/分组 ingress 不重跑；因渠道范围延后的 ingress 每次匹配尝试都会运行。HTTP 晚 ingress 从本次映射后的上游 JSON 开始，不能改变模型、流模式或已经估价的计费成员，只允许原子 JSON patch，不允许业务头或媒体效果。未选中该账号时不执行其规则；它不会在早期入口施加 JSON/无附件限制。
- 规则拒绝、必选失败、审计失败都不会发送生成请求、换账号或处罚上游；按现有本站策略错误格式返回，拦截仍是 403、执行失败 503，与 New API 原生 400/500 状态存在差异。账号后来移除 Provider 绑定仍执行适用原生规则；实际协议未接通时先失败，不绕过规则。此类错误排除模型上游故障采样。
- 单 JSON 1 MiB、投影深度 64/节点 16384、媒体描述 64 项；每阶段五秒（含共享四进程槽的等待），审计另两秒，仍服从 Provider 初始化/凭据更新总预算。先预留共享 ingress 内存，再分配暂存投影；阶段暂存随尝试释放，实际请求正文保留到该响应结束。无需常驻 worker/后台队列，不嵌套租用 Provider 进程槽。

执行记录增加账号 ID/尝试号，继续只保存宿主定义的阶段、动作、错误类、耗时和数量。试运行选择这些阶段时使用对应 Chat/Responses/Claude JSON，只执行单阶段、不读取实际账号或 Provider、无 HTTP/费用；outbound 返回排队业务头便于核对。report 不参与试运行。

## Task JSON egress

Task 提交顺序与固定 New API 基线一致：客户端 ingress → 原有准入、Task 解码与估价 → 任务及费用冻结同事务 → 后台 Task 插件构建提交参数 → egress → 上游生成请求。Task 不执行普通 relay 的 request、outbound 或 reply。

- 实例阶段可选 ingress、egress 或按顺序选择两者。Task 实例包含 egress 时，`formats` 必须明确为 `["task"]`，避免误影响普通文本调用；HTTP egress 必须另建明确 Chat/Responses 格式的实例。旧实例默认 `formats: []`，保留原先全部格式的 ingress 行为。
- 全局规则先于分组规则。egress 每次真正提交尝试前运行，看到 Task 插件生成的上游 JSON，不重新执行 ingress。当前持久队列不会因 egress 拒绝而换账号重试；轮询和下载不再检查。
- egress 只读，允许通过、拒绝和 findings；任何非空 effects 都是执行错误，不能修改最终请求、价格或认证信息。显式拒绝不能被 `meta.optional` 跳过；optional 仅允许跳过执行失败。
- 上游 multipart 不进入 egress，按基线保存 `unchecked` 记录。校验的是构建后的 JSON 参数，不提供业务或认证头。内嵌附件与允许有界内联的持久 `__fileRef` 可使用受限媒体工具；后者只读并校验归属与快照。`utils.fetch` 需按上节显式开启。
- 创建时固定 egress 实例、配置、版本 ID 和源码 SHA256，加密保存在任务中。快照正文最多 256 KiB，密文最多 512 KiB，不复制源码或请求正文。管理员后续修改/停用实例、切换插件默认版本、重启服务都不会替换已接受任务的规则。升级前任务保持无 egress 的原约定。
- 运行总预算 5 秒（含数据库版本读取和进程排队），执行记录另有 2 秒写入截止。运行 Task 构建钩子后先释放进程槽，再执行 egress，避免嵌套占用四个 JS worker。必选规则失败、快照异常、拒绝或审计失败均不发起生成。
- 拒绝或失败作为确定未提交的终态持久保存，下一次后台结算释放原冻结金额和原额度预记，通常约 10 秒后推进；数据库故障时持久队列继续恢复，不承诺立即退款。退款复用现有财务事务和幂等记录，不会再次调用上游。

内置 `request-redact` 可建立仅 egress 的 Task 实例，配置如：

```json
{
  "categories": ["email", "api_key"],
  "finalCheck": "reject",
  "replies": false
}
```

它检查插件构建后新增或仍残留的敏感文本。若还需要入口脱敏，选择 ingress → egress；JSON 内嵌附件可读取和改写 UTF-8 文本；PDF/DOCX 只读；持久文件和视频 multipart 按上述阶段与大小限制处理。纯 egress 实例不会在客户端入口套用无附件限制。

## Task 完成报告 report

报告按固定基线在任务最终结算/退款事务成功之后执行；成功、明确失败、排队超时和人工对账后的终态都可以报告。提交时不报告，轮询/下载不再次报告，试运行也不执行 report。Task 的 `report` 实例（包括只导出 report 的脚本）必须明确限于 `formats: ["task"]`。普通 Provider HTTP report 见后文；WS 和旧原生任务报告仍未接入。

- 任务创建时仅固定匹配的实例 ID、入口后的客户端 JSON 和安全执行记录；报告时读取这些实例**当前**的启用状态、固定版本和配置，不再按新分组/模型范围重匹配。禁用实例可以停止后续报告。这与 egress 固定原配置的行为不同，遵循 New API 的 Task report 约定。
- 客户端 JSON 报告快照最多 64 KiB；inline data 媒体替换为 `[media:N]` 并记录 MIME/字节描述，超过限制、非 JSON、直接 multipart 或带私有预上传引用的请求体为 null。快照加密，最多 128 KiB 明文/256 KiB 密文。快照生成先申请共享 ingress 内存预算，失败只丢报告并计数。旧任务不回填原正文或实例。
- record 包含请求 ID、用户/Key 名、任务创建至首次终态的时间、请求投影、执行记录、最终费用及任务结果。没有 response、usage、请求头、IP、账号凭据、原始插件 state 或采购价格。`cost.usd` 为本站实际结算美元金额；`cost.quota` 按固定基线默认每美元 500000 单位表达，仅供兼容报告读取，不修改本站额度账本。
- 从已保存的任务结果中收集 http/https URL（去重、最多 32 个）和其他文本（最多 256 条/1 MiB），省略 data URL 与长 Base64，超过限制标 truncated。Task 插件在完成结果里提供 http/https 主 URL 时，将其随私有报告快照保存并优先排列；其余 URL 从保存结果中去重提取，旧任务不猜测回填。报告整体还受 runner 的 1 MiB 参数上限，超限丢弃并计数。
- 2 个固定 worker、32 个排队任务、总计 8 MiB（包含正在运行的任务）；入队先预留再复制，仅保留明确的不可变事实。满载/关闭/快照失败可丢弃并统计，系统告警每分钟限流。每个实例读取数据库 2 秒，执行总预算 35 秒（包括受限网络）；关闭取消执行、释放队列，不无限排空。没有逐任务后台 goroutine，也没有自动回调重试。
- report 提供受限 `utils.fetch`，只允许 meta.allowedHosts 和当前已校验实例 schema 的 format:url 主机，验证公网 DNS，禁止重定向和 URL 凭据/fragment，最多 4 次调用、每响应 1 MiB、每次 15 秒并服从 hook 总预算。使用独立无账号凭据的请求，不复制客户端或上游认证。管理员设置的实例配置可显式提供回调认证。
- 报告是尽力通知。结算成功到入队之间崩溃、队列过载、服务关闭可能丢报告；报告失败或丢弃不回滚账务、不改任务结果、不重新生成。它不是可靠支付通知/outbox。`GET /api/v1/admin/request-processing` 返回当前节点的 accepted/completed/dropped/failed 计数，completed 按任务处理计，failed 按实例失败计。详细执行异常不保存正文或 URL。

管理员配置 report 就是在允许插件读取并发送上述请求/结果；页面会展示此说明。可用如下最小插件：

```javascript
export const meta = {
  apiVersion: 1, key: "community-<your-namespace>-task-report",
  name: "Task completion report", version: "1.0.0", author: {name: "Your name"},
  configSchema: {type: "object", properties: {
    url: {type: "string", title: "Report URL", format: "url"}
  }, required: ["url"]}
};
export function report(ctx, record) {
  utils.fetch({url: ctx.config.url, method: "POST",
    headers: {"Content-Type": "application/json"}, body: JSON.stringify(record)});
}
```

## 内置示例

`request-filter` 入口关键词过滤：

```json
{
  "keywords": "仅限内部使用\n不要外发",
  "replies": false
}
```

`request-redact` 入口邮箱/API Key 脱敏：

```json
{
  "categories": ["email", "api_key"],
  "replacement": "[REDACTED]",
  "finalCheck": "off",
  "replies": false
}
```

过滤/脱敏的 `replies: true` 需要在已支持的 HTTP 作用范围内选择 reply 阶段；未选择 egress 的入口实例中，脱敏的 `finalCheck` 必须显式设为 `off`。Task 实例选择 egress 后可以启用 `finalCheck: "reject"`。`content-moderation` 需要显式开启实例联网，并配置允许的服务 URL；离线试运行仍不会发送审核请求，真实审核服务需独立联调。

## 最小社区入口插件

在“插件开发”创建草稿时，key 使用页面分配的 `community-<用户ID的base36>-` 前缀；通过管理员审核、发布和安装后才能新建生产实例。下面的 key 需要替换为自己的命名空间。

```javascript
export const meta = {
  apiVersion: 1,
  key: "community-<your-namespace>-prompt-filter",
  name: "Prompt filter",
  version: "1.0.0",
  author: { name: "Your name" }
};

export function ingress(ctx, req) {
  if (typeof req.body.prompt === "string" && req.body.prompt.includes("internal-only")) {
    return { action: "reject", code: "private_prompt" };
  }
  return { action: "continue" };
}
```

这个示例只检查顶层 `prompt`。Chat/Responses/Gemini 的消息结构不同，需要自己处理对应字段，或使用已包含这些结构的内置过滤插件。此例没有申请联网；入口需显式开启受限联网，所有离线试运行始终禁网。

## 视频幂等与费用

原始客户端 JSON 的规范化哈希决定 `Idempotency-Key` 身份。处理后的请求决定协议解码、提交参数、价格估算和冻结。规则随后变更时，已接受的同键同正文重试返回原任务，不重新执行规则、选上游或冻结；同键改变客户端正文仍返回 409。

试运行不调用模型，不收取生成费用。实际调用只有通过全部入口规则并完成原有准入后才创建任务和账单。入口插件不能直接操作资金。

## 日志与隐私

处理执行记录与视频“任务日志”分开：前者记录规则执行，后者记录生成任务与账单进度。

执行记录只保存宿主定义的动作、阶段、版本、错误类别、耗时和计数，不保存请求/回复正文、密钥、实例配置、插件异常原文、命中内容或拦截提示。插件提交与试运行正文也不写入通用管理审计。列表查询不读取源码和密文配置；完整配置使用受二次验证保护的独立接口读取，响应禁止缓存。

入口/提交前执行记录或回复开始记录写入失败时返回 503，不继续调用模型。回复的结束汇总发生在可能已经发出内容之后：失败时保留开始记录、增加节点失败计数并返回内部失败事实，无法撤回已发出的字节。下游收到宿主固定错误文案，避免插件自定义错误泄露私有内容。入口及 Task egress 拒绝标为本地策略原因，不计作模型上游故障。

## 管理接口

均在现有管理员认证下，前缀为 `/api/v1/admin/request-processing`：

| 方法 | 相对路径 | 用途 |
| --- | --- | --- |
| GET | 空 | 不带配置的实例列表、支持范围 |
| POST | 空 | 新建/修改实例，需要 revision 与二次验证 |
| POST | `/:id/config` | 读取完整配置，需要二次验证 |
| POST | `/dry-run` | 未保存配置的单实例试运行，需要二次验证 |
| GET | `/runs` | 按 request_id 分页查询执行记录 |

试运行可选择 `stage: "ingress"`（默认）、Task `egress`，或 Chat/Responses/Claude 的 request/outbound/egress/reply；`late_ingress` 专用于账号 ingress 的已估价上游 JSON 试运行，输出审计阶段仍为 ingress。普通 `ingress` 试运行保留客户端估价前语义，界面按账号作用域自动选择。reply 输入为模型的 JSON 响应正文；其他阶段分别使用客户端或构建后的上游 JSON。它不模拟渠道选择、建任务或资金冻结，完整生命周期仍需宿主测试。

每套宿主最多 64 个实例；每个实例最多 100 个分组或模型。数据库迁移 `257_request_plugin_instances.sql` 与 `266_task_request_plugin_egress.sql`、`267_task_plugin_reports.sql`、`268_provider_request_plugin_attempts.sql`、`269_provider_reply_processing.sql`、`270_provider_http_reports.sql`、`272_provider_late_ingress.sql`、`273_request_plugin_stage_network.sql`、`274_global_group_http_reports.sql`、`275_request_plugin_reply_network.sql` 随启动执行。生产启用前先停用保存和试运行，再逐分组开启；紧急回滚可停用对应实例，等待各节点刷新。

## 普通 HTTP 完成报告 report

全局/分组支持 `report` 单阶段或与早 ingress 组合，明确选择 `openai`、`openai_responses`、`claude`、`gemini` 中的格式；Task 必须单独选择 `task`，旧实例保持原范围。Chat/Responses 可组合已支持的 HTTP 请求与回复阶段。Messages 与 Gemini generateContent/streamGenerateContent 的实际 JSON/SSE 输出及原生 Anthropic、Gemini、Antigravity 转换路径已接报告；指定原生 Anthropic 账号且限定 claude 格式时，可组合已接通的六阶段；Gemini/Antigravity 账号可组合已接通的 HTTP 六阶段；实际晚阶段读取 Gemini wire，透传账号按其只读/unchecked 约定执行。账号仍从现有账号管理选择，格式是客户端协议。一次客户端请求按全局→分组→最终匹配账号报告；续期只报告最终尝试，换号清除旧账号的目标、用量和账本句柄。WS 和其他协议报告仍未开放。沿用固定 New API 基线的普通 HTTP 报告结构，**不表示整个 New API 宿主完全兼容**。

- 固定请求开始时的版本、源码、配置和位置；禁用只影响后续请求，已经开始的普通 HTTP 报告使用原快照。路由前失败可发送全局/分组报告，此时不提供 channel；选定账号后提供最终账号 ID 和固定基线的渠道类型，无对应平台为 Unknown（0），Provider 为 TaskPlugin（61）。此映射也用于 `meta.scope.channelTypes` 匹配；选渠道前的失败没有 channel，不执行带渠道范围的报告。Task 报告仍按上节在完成时读取当前配置，两者遵循基线的不同约定。
- 后台只接收不可变投影：请求 ID/用户和 Key 名称、入口处理后的客户端 JSON/素材占位、实际发给客户端的响应、阶段执行记录、用量和账本事实。firstResponseMs 为首次实际写出正文到捕获 writer 的耗时。没有 Gin context、writer、HTTP 请求、账号对象、上游连接或凭据。回复脱敏在捕获之前运行，报告看不到被遮盖的模型原文。
- Messages/Gemini 复用原 `ForwardResult` 和同一次原账本结算；`promptTokens`、`completionTokens`、`cacheTokens` 保留该转发结果的实测口径，不重新按公开响应猜测。可用时另外提供 `cacheCreationTokens`、`cacheCreation5mTokens`、`cacheCreation1hTokens` 和已有图片统计。部分流结果也需原账本确认；不新增扣费、用量行或后台队列。Bedrock 使用固定渠道类型 33，Antigravity 无对应类型时仍为 0。
- `cost.usd` 只取本次成功提交的本站账本实际费用（8 位小数），`quota` 按每美元 500000 换算并四舍五入到兼容的整数单位（不回写账本），**不是采购成本或上游报价**。简单模式已确认不扣费报告 0；没有任何计费结果的失败报告 0。有结果则等待异步计费；结算失败、重复结算未能确认原金额、旧无账本回退或等待超过 15 秒都丢报告并计数，不能拿重算价格或猜测的 0 冒充结算事实。报告超时不取消账务。
- 请求投影最多 64 KiB、响应捕获前 1 MiB，bytes 是客户端实际写出字节数，超出标记 truncated；截断 JSON 不提供 body。SSE 只保留完整帧，最多 1024 个数据事件、单帧 4096 行，忽略 DONE 和心跳；半帧不能伪造成完整事件。JSON 深度 64、16384 节点和重复 key 保护；异常文本作为字符串，二进制不进入报告。
- 捕获先从共享 ingress 预算预留 12 MiB（准入账面值，不是 RSS 限额）；最终最多 16 目标/256 阶段记录。与 Task 共用两固定 worker、32 队列项及 8 MiB 排队/在途数据账面预算，先准入再复制并计入保留源码/配置；大报告可能因容量不足跳过。每个实例最多 35 秒，受限 fetch 与 Task 相同；关闭取消等待和 HTTP，释放队列，无无界 goroutine 或重试。
- 管理页显示当前节点自启动 accepted/completed/dropped/failed。completed 表示已处理，包含执行时跳过/丢弃，可以与 dropped 重叠；failed 按失败钩子计数，不是送达确认。报告为尽力通知，可在过载/关闭/进程崩溃时丢失，不是耐久账本或回调 outbox。它不影响模型响应、账号健康或已有扣费队列策略，试运行不发送报告。

## 原版参数覆盖插件

`param-override` 现在可作为指定 Provider 账号的 outbound 实例，执行固定 New API 基线的参数覆盖 DSL。支持旧式顶层字面 key 和 `operations`，包括条件/AND/OR/反选/缺字段、负索引/通配符、复制/移动/数组与字符串操作、合并/裁剪、请求头操作/字段同步及 `return_error`。直接移植规则实现和纯规则回归；不引入 New API 的账号库。

例如实例配置：

```json
{"override":{"operations":[{"mode":"set","path":"temperature","value":0.7,"conditions":[{"path":"original_model","mode":"prefix","value":"gpt-"}]},{"mode":"set_header","path":"X-Request-Source","value":"gateway"}]}}
```

条件上下文与基线 `utils.paramOverride` 一致：公开/上游模型、当前分组、请求路径、格式、尝试号及账号 ID/类型；不含客户端或账号认证头。DSL 返回的效果仍通过宿主原子 patch 和计费/模型/认证保护，不会因为使用工具函数而绕过限制。`return_error` 只形成原版 Transform 拒绝结果；本站沿用明确拒绝 403、必选错误 503，不采用规则里的任意 HTTP 状态或把异常/拒绝文案回传给下游。

本地资源边界：正文/结果 1 MiB、规则 64 KiB、最多 128 条 operations 或旧式字段、JSON 64 层/每个输入或写入值 16384 节点、路径 8192 字节、数组新增索引最多 16384、正则 4096 字节/32 捕获组/一次替换最多 16384 匹配。正文、字面替换和捕获展开在结果放大前或每次有界编辑时检查；正则不保留插件可控的常驻缓存。仍共享四个子进程及原阶段五秒总预算，超限按插件错误处理。已有对象的数字 key 保留字面语义，不按数组长度处理。

当前仍限已接通的 Provider 账号 outbound；原脚本名称中的“全局”不意味着本站已经开放所有内置账号/平台或全局/分组普通出站。该覆盖范围和完整 New API 的差异继续以兼容表为准。


## HTTP 回复检查

全局/分组及指定原生或 Provider 账号的实例可选择 `reply`。包含 request/outbound/egress 的组合仍只对指定 Provider 账号开放。使用原版 `request-filter` 或 `request-redact` 时，在配置中设 `replies: true`；未选择 egress 仍需将脱敏的 `finalCheck` 设为 `off`。原有未选择 reply 的实例不会因升级自动检查回复。

回复检查位于原生或 Provider 响应转换和公开模型名替换之后、向客户端写出之前。JSON 一次检查最终文本；SSE 按原版逻辑用 UTF-16 偏移、少量暂存、跨片段识别和 `***` 遮盖，Responses 的 done/full-text 镜像同步改写。插件不能任意插入回复，也不修改原用量。当前仅普通 Chat/Responses 文本，不含 WebSocket、工具参数、思考文本或生成的媒体；不可读取的媒体/错误响应记录为 unchecked，不冒充检查通过。

显式 stop 返回固定安全错误（JSON 403，已开始的流发 SSE error），必选插件错误或资源超限返回固定失败（JSON 503，流内 error）；不回传插件的私有 message/异常。流停止会取消本次实际 HTTP 连接、不换账号、不补 `[DONE]`。已经从上游收到的用量仍通过原账本结算；未知用量不会造数。可选插件执行错误可跳过，显式 stop 不能被 optional 绕过。本地策略失败不处罚上游账号或算进模型故障率。

资源边界：每个请求最多 16 个回复实例、64 个输出或正文文本段；每个 JSON/事件或累计待检查文本最多 1 MiB，JSON 深度最多 64 层/16384 节点且拒绝重复键；暂存事件最多 256 项/4 MiB 原始字节，总事件最多 65536；回复 hooks 合计最多 4096 次、累计执行含排队 10 秒、单次最多 5 秒。只复用固定子进程槽，没有每请求后台队列；联网需独立授权，并另行预留临时内存。整个 forward scope 预先从共享 ingress 内存预算预留 64 MiB，用于实例暂存、解析事件的多种表示和 RPC；scope 结束释放。这是准入账面预留，不是操作系统 RSS 硬限制。

每次实际上游尝试先保存 `started`；凭据拒绝后重建时，旧尝试记 unchecked、新尝试再次保存 started。回复完成只存每个实例一条动作/遮盖/命中/耗时汇总，不逐 Token 写数据库；失败汇总计数在请求处理页显示，按当前节点自启动累计，服务日志按计数限频。进程崩溃可能只留下开始记录，不能把它当作成功或宣称无损审计。汇总失败不会撤销已经完成的账本。

reply 的离线试运行直接使用 JSON 响应正文，不模拟上游、账号或账单，也不联网；流式暂存需走真实网关的模拟上游回归。WS 及其他协议的回复检查仍未开放；request/outbound 按固定契约不提供 fetch，不能把本批当作完整 New API 宿主兼容。

迁移 `276_global_group_http_replies.sql` 放开全局/分组的显式 HTTP reply，以及与早 ingress、HTTP report 的组合；旧实例不会自动增加回复阶段或联网权限。reply 必须明确限定 Chat/Responses，不能和 Task egress 混用，不包含 WebSocket 或媒体内容检查。
