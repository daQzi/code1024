# New API 插件接入与验收状态

更新日期：2026-11-23。已加入官转二、官转五专用视频插件，配置说明见 `CHANNEL_ADAPTERS.md`。

已提供 `sub2api-openai-video` 通用视频插件，同协议上游通过账号配置即可接入。使用步骤、价格和开发规范见 [`VIDEO_PLUGINS.md`](../../../docs/customization/VIDEO_PLUGINS.md)。运行时、持久任务执行器、插件/市场/查询领域及框架无关的 HTTP 回复过滤、媒体检查/编解码已提取为仓库根的 [`newapi-engine`](../../../newapi-engine/README.md) 独立 Go 模块；管理 UI 组件位于 `frontend/packages/newapi-ui`（已通过独立构建与 Vitest），生产由 `newapihost` 单点装配并通过 `newapi_engine.enabled` 开关控制停用/回退边界，准确边界见 [`NEWAPI_ENGINE_DECOUPLING.md`](../../../docs/customization/NEWAPI_ENGINE_DECOUPLING.md)。

## 当前能够使用的路径

```text
插件管理：安装 JS → 启用固定版本
账号管理：普通 API Key → 地址 / 静态 Key 或插件登录 → 选择 Task 或已支持的 Provider 插件 → 分组 / 模型映射 / 定价
下游：创建 /v1/videos → 查询任务 → 下载 content
后台：持久轮询 → 记录最终用量 → 结算 / 退款 / 人工对账
模型广场：卡片或表格 → 独立详情 → 分组价格 / 接入示例 / 真实完成时长
```

不需要新建一套“上游连接”数据库。账号沿用现有代理、分组、优先级、并发和 Key。
现有 OAuth Transport `.s2plugin` 使用独立“原生扩展”页面 `/admin/native-plugins` 管理，执行仍沿用原生运行时；New API 插件中心为 `/admin/plugins`。

插件中心现提供系统/社区市场、版本详情、兼容性阻断和恢复内置。21 个内置版本自动登记但默认停用，不替换已有激活版本。用户在“插件开发”中提交命名空间隔离的源码，经管理员审核发布后才能进入本站市场；安装按固定 SHA256 进行，且不隐式启用。独立“请求处理”页面现可配置全局/分组 JSON 入口及 Task 提交前检查实例、固定版本、格式范围、排序、试运行与执行记录。外部 New API API v1 市场现可配置官方、社区与自定义来源，按固定版本查看源码/文档、检查撤回与 SHA256 后安装；安装不隐式启用，详情见 `MARKETPLACE.md`。

## 插件基线

- 来源：QuantumNous/new-api。
- 固定提交：`48f9df96ea7c68eae04a0f3687869f232f48929f`。
- 保留 17 个原版内置 JS 脚本，另有 4 个本项目适配脚本、契约代码及来源许可。见 `NOTICE.md`、`LICENSE`。
- JS hooks 在宿主子进程运行，最多 4 个进程。进程隔离不等于操作系统沙箱。
- 支持源码安装、不可变版本、摘要、启停、默认版本切换和账号固定 revision。
- 插件详情支持系统工厂源码、已安装固定版本源码查看及同 key 历史版本逐行对比；市场下载候选也可与本站安装版本比较。服务端和浏览器核验原始 UTF-8 摘要，大源码保留完整只读查看，行级对比有大小/行数上限；不自动合并或安装，详见 `MARKETPLACE.md`。
- 切换默认版本不会自动迁移账号；已有任务固定原 revision、配置、模型映射、目标地址和价格。
- 旧版已绑定账号若要开启本批新增的 Images、Audio、Responses 或 `meta.routes`，部署后在账号管理用原版本重新保存一次，刷新派生的调度能力；保留任务原快照。
- 账号的 Key 可以轮换；修改账号地址不会迁移已经接受的任务目的地。
- 新功能启用沿用现有管理员审计和二次验证。

## 实际宿主支持范围

| 能力 | 状态 |
| --- | --- |
| 原版脚本编译、meta/schema、typed hooks 校验 | 已移植 |
| Task 的 `openai_video.decodeRequest/render` | 已接入 |
| `buildSubmitRequest/parseSubmitResponse`、查询、终态用量、产物声明 | JSON HTTP 与 SSE 提交聚合已接入 |
| Provider `exchangeKey/signRequest` | 已接到 Task 提交、查询及鉴权下载 |
| 视频 Task 登录与凭据维护 | Task-only 插件可使用同一 login/refresh/revoke/scheduled 卡片、密文存储与维护队列；新任务固定非机密登录身份、事务重查及重启校验，提交/查询/受保护下载使用 credential headers/query 后再签名；匿名身份刷新保持、重登更换，父任务必须同一登录，旧静态任务不自动领用 grant；不自动重试付费提交。见 `TASK_CREDENTIALS.md`；Google `meta.auth.oauth2_jwt` 已接宿主有界 RS256 换票，Task 只接收授权 Header/projectId；网关上游直接用 Bearer Key，见同文 |
| 受限 `utils.fetch` | 已接入，最多 4 次/hook、1 MiB/响应、15 秒/请求，禁止重定向 |
| `channelConfig` 默认值、校验、密文保存 | 已接入；只对 schema 的 URL 字段扩充允许目标 |
| Grok/xAI、Ark/Seedance、Sora | 原版脚本模拟闭环已验证；Ark 支持映射到 `ep-…` endpoint；真实调用待验证 |
| 旧 Grok / Ark 原生视频历史 | 新提交的原生视频已接持久任务日志和私有计费输入；Redis owner/pending 缺失时可按完整归属恢复原账号与参数，歧义或缺证据时拒绝恢复/扣费；有连接摘要的普通 API Key 新任务可后台跟进状态，有完整价格/连接证据的新任务可用实际用量后台结算，费用仍以原账本成功凭据为准，未整体改为 JS Task 驱动；见 `NATIVE_VIDEO_RECOVERY.md`、`NATIVE_VIDEO_MONITORING.md` |
| 已绑定插件的原生视频入口 | xAI 生成/编辑/延长及 Ark 创建/查询，已通过固定 `native.decode` 接入共同 Task 队列、后台查询、资金和日志；保留模型映射、多素材与原生响应形状，本站公开 ID 与受限宿主 DTO；配置及切换边界见 `NATIVE_VIDEO_INGRESS.md` |
| 官转二、官转五 | 专用 Task 插件（`OpenAI Videos -> 原生 API`），内置历史版本与 `community-plugins/` 独立社区发行包（`community-sub2api-dreamina@1.0.0` / `community-sub2api-official5@1.1.0`）并存；模拟回归与 `pluginctl` 离线 fixture 已验证，内置源码及旧任务零改动 |
| 普通 Provider relay | OpenAI 平台静态 API Key 或服务端保存的 Provider 登录凭据、原生 Chat / Responses 文本 JSON/SSE、精确/正则模型路由、路由认证、配置 URL、`prepareRequest` 和 `signRequest` 已接入；内置 `sub2api-openai-relay` 可选 |
| Provider `converter: plugin` | `convertRequest` → `prepareRequest` → 签名 → JSON 响应或 SSE/NDJSON 事件转换已接入；响应固定同一请求的源码、配置与 state；下游仍为标准 Chat / Responses |
| Provider Embeddings | `/v1/embeddings` 的 none/plugin JSON 已接账号候选、固定连接/凭据、签名、next、一次续期及既有输入用量/账本/监控；明确输入用量缺失或非法时失败，不按免费成功。响应最多 1 MiB，不接生成参数/流式；已接 Transform 请求阶段与 report，按固定基线跳过文本 reply；模型广场仅按实际声明显示接口及示例 |
| Provider 预置及组合转换 | 直接依赖相同固定提交的 RelayKit，独立引擎 `protocolconv` 已支持全部 12 条文本协议转换器（Chat / Responses / Claude Messages / Gemini 互转），宿主公开入口已接通 `/v1/chat/completions`、`/v1/responses`、`/v1/messages`（`none`、受校验预置及 `<preset>+plugin`，JSON/SSE）及 `/v1beta/models/*`（OpenAI 分组显式声明的 `generateContent` / `streamGenerateContent`，原生 Gemini/Antigravity 分组及未声明模型/动作保持回退原网关）；预置先转换请求，插件还原预置的上游协议后再转回客户端。Anthropic `server_tool_use`、xAI `server_side_tool_usage_details`、GLM `web_search`（含 `search_std`/`search_pro*` 及 `search_intent:false`）、阿里 DashScope `enable_search`/`search_options`（`search_strategy_*` 与 `usage.x_tools.web_search.count`）、上游 Responses 托管调用与 Gemini grounding 查询证据均在 codec 前保留，hook 明确值优先且防重计费。单份/流累计 JSON 1 MiB、8192 节点/64 层；流缺真实终态不调用成功 finalizer；Anthropic 目标需显式输出 Token 上限，不猜默认值；Gemini 失败前未写首块返回标准 Google JSON 错误，流中异常补标准 Gemini SSE 错误帧 |
| Provider 静态凭据 | 保存新增或修改的连接时执行 `checkKey`；`exchangeKey` 使用单节点有界缓存、并发合并和 30 秒过期提前量；401 或 `credentialRejected` 明确拒绝后精确失效，最多重建并重试一次；沿用现有账号异常处置 |
| Provider 链式请求 | 已接最多 32 个 JSON 后续请求；逐步独立认证/签名、state 固定、首步 prepare/Transform 不重放、全链最多一次凭据续期且只重试当前步骤；进入 next 后失败及聚合变更流的晚到凭据错误禁止外层换账号/账号池重发整链；JSON 握手可接最终 SSE，完整生成只在最终标准响应计量；前缀总预算 2 分钟，不限制已选最终流的健康总时长；不等于持久幂等或无最终结果的费用恢复 |
| Provider 流转 JSON | 上游 SSE/NDJSON 可由 convertStreamEvent 返回完整 body 或显式 JSON chunks，聚合后经原 Chat/Responses 校验、日志、用量与账本；不自行猜测/拼接厂商 delta。总正文 1 MiB、整个聚合受原 2 分钟链预算限制，取消/错误关闭真实连接；已出现上游事件的 POST 等变更请求，晚到的凭据错误不自动重放 |
| 插件模型发现 | 已保存的静态 Key 或已登录 Provider 插件账号可手动通过显式 `/v1/models` relay 读取真实上游模型；支持 none/plugin、路由鉴权、签名、最多 32 次 next 及全链一次 401 续期；只返回最多 4096 个模型 ID，不更新账号映射/能力/价格或公开目录，不调用平台内置发现 |
| 插件账号上游余额 | 已保存静态 Key 或已登录 Provider 账号可手动执行 `buildBalanceRequest/parseBalance`，展示金额/无限额/额度窗口；固定 revision、配置、代理及 Key，逐步签名，最多 32 次后续请求，401 全链最多一次当前步骤凭据续期；不写本站余额、价格、健康或路由，不自动轮询；编辑页支持读取已固定的历史版本能力 |
| Provider 登录与凭据维护 | 已接 login/refresh/revoke、字段/回调/设备步骤、密文持久状态、会话归属与 CAS、多实例续期租约、静态交换令牌的定时维护及显式自动暂停开关；适用当前 Provider 文本路径及管理查询，视频 Task grant 接点见单独一行。删除/解绑/换连接的持久撤销及管理员清理重试已接；Chat/Responses 调度候选、粘连和最终重查已加入公开凭据状态过滤；原生认证/永久错误/重复超时停用事件及显式允许的原子恢复已接；维护通知已接到既有 Ops 邮件配置与统一模板（保存后尽力投递）。详见 `PROVIDER_CREDENTIALS.md` |
| Provider 工具费用 | 任意已定价 hook tools 增量、显式零、逐 key 上限、整链累计，及公开 Chat/Responses 的有界实际调用计数/去重已接唯一账本；插件中心有 USD/千次的逐模型前缀价格表、CAS/二次验证和请求固定价，不导入厂商默认币种报价；迁移 288 保存版本化历史售价/基础倍率/已包含费用，用户与管理用量页可查看，旧记录保持未知；私有工具用量仍有限制，见 `docs/customization/PROVIDER_TOOL_PRICING.md` |
| Provider Images / Audio | 已绑定账号可接 Images generations/edits 和 Audio transcriptions/translations/speech；有界表单（32 MiB / 64 part 上限、共享请求内存预留、Audio multipart 拒绝压缩 `Content-Encoding` 415）、`file` 原始字节、`$file` 内联与带状态的 next 走固定凭据和实际正文签名。二进制音频响应统一输出 `X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer` 与 `Content-Security-Policy: sandbox`。Audio 只支持非流式，支持明确平价按次售价（含显式零）及按字符数（TTS `/v1/audio/speech`）/按实测时长（STT `/v1/audio/transcriptions`、`/v1/audio/translations`）分组计费；缺失输入或缺失/非法实测时长一律 fail-closed 拒绝，绝不猜测或免费放行，使用原账本 |
| Provider WebSocket | 上游插件 WebSocket（`convertRequest` 返回 `websocket` + `convertStreamEvent` 文本/二进制帧往返与 `close`）与客户端原生 Responses WebSocket（`/v1/responses` WS 接入原生 Provider relay 路由）均已离线接入；单会话独立连接池隔离与断开清理、握手签名与非 101 响应体捕获触发单次 `exchangeKey`/`credentialRejected` 续期、禁透传下游认证及 Codex 内部头/元数据、首轮映射后固定公开与上游模型（切模型以 `StatusPolicyViolation` 拒接）并按轮次回填真实用量与唯一账本计费；真实上游联调随 R8 |
| Transform `ingress`：全局/分组、精确模型范围、JSON Patch | 已接入 Chat Completions、Responses、Images JSON（含缺省模型）、Messages、Gemini 生成、`/v1/videos` 和受支持的原生视频创建入口；单请求只运行一次 |
| Transform `meta.scope.channelTypes` | 已按实际选中账号的固定 New API 类型筛选 HTTP 六阶段及 Task egress/report；类型未确定/没有对应映射时不匹配，带渠道范围的 ingress 在选中后延迟执行，早已执行的不重跑；Task 晚 ingress 在最终解码/估价/冻结前执行，标准视频客户端 multipart 使用原上传文件的只读投影；此类 ingress 实例需显式选择 Chat/Responses/Claude 或 Task 格式，Gemini 等其他协议晚 ingress 尚未接入 |
| Transform Task `egress` | 已接入持久 Task 的 JSON 提交前只读检查；冻结后、生成网络前执行，快照固定原规则，拒绝/必选失败确定未提交并退冻结；JSON 内嵌附件可经宿主有界读取；描述符 multipart 字段和持久 fileRef 与 JSON 内联引用均可使用只读附件工具及单独授权的检查服务；只有 opaque 文本或无可检查分片的正文记录 unchecked，仍不修改原提交内容 |
| Transform Task `report` | 已接最终结算/退款后的有界后台报告，固定创建时实例 ID、报告时检查当前启用/配置；有限客户端/结果投影和受限联网，尽力投递，不是财务 outbox；详见请求处理文档 |
| Transform HTTP `request/outbound/egress` 与账号晚 `ingress` | 已接全局/分组/当前原生或 Provider 账号的 Chat/Responses JSON/SSE 与 Embeddings JSON；Embeddings 跳过生成策略并拒绝 reasoning.set；request 在原生协议转换前，晚 ingress/outbound/egress 在原生实际出站 Chat/Responses JSON 上；Provider egress 仍在 convert/prepare 前。按尝试固定规则，支持 reasoning.set、JSON/业务头原子 patch 和只读 egress；透传晚 ingress/outbound 记 unchecked，未接通的原生协议带规则时发送前拒绝 |
| Transform HTTP `reply` | 已接全局/分组及指定原生或 Provider 账号的 Chat/Responses 实际下游 JSON/SSE 文本；原生 Anthropic、Gemini 与 Antigravity 的 Messages/Gemini 回复也已接入，原生转换仍由原网关执行；原版 UTF-16 暂存/遮盖/停止、全量文本镜像重写；最多 16 个实例，执行/内存/队列有界，停止会取消实际 HTTP 并保留已计量用量，先记开始后记汇总 |
| Transform 原生 Images | API Key、OAuth/setup-token 的 generations/edits JSON 已接只读 request、晚 ingress、outbound、只读 egress 和原账本确认后的 report。API Key/direct 晚阶段仅可改 prompt/user；OAuth Responses 桥接使用实际 openai_responses JSON，仅可改 input[0].content[0].text/user，图片模型/工具/规格/素材及内部文本控制固定。OAuth 规则在换票/发送前执行，原生身份/协议头受保护；direct 回退重新构造并执行，拒绝立即停止。内部文本 reasoning 不冒充图片计费等级，JSON/SSE 媒体结果不进入文本 reply；multipart 带请求阶段仍明确拒绝。模型身份沿原缺省/合成路由/渠道/账号映射；详见 REQUEST_PROCESSING.md |
| Transform Provider HTTP `report` | 已接指定 Provider 账号最终 Chat/Responses/Embeddings 调用报告；入口后客户端投影、实际下发 JSON/SSE、用量与已确认账本费用；固定请求时版本/配置，复用 Task 有界后台 executor/受限 fetch；未确认/重复结算或等待超时丢报告且计数，不伪造零费用 |
| Transform 全局/分组 HTTP `report` | 显式限定 OpenAI/Responses 格式，可报告现有原生账号及 Provider 的最终 Chat/Responses/Embeddings 及原生 Images JSON/SSE；全局→分组→最终匹配账号，原生账号可建立独立 report-only 实例；路由前失败没有 channel，换号清除前次目标/用量/账单句柄；沿用原完成报告与账本确认边界，无额外后台队列 |
| Transform `ingress/egress` 联网 | 已接当前支持的全局/分组客户端 ingress、原生/Provider HTTP 晚 ingress/egress 及 Task JSON egress；实例显式开启、默认关闭，实际源码允许主机及配置 URL 限定、公网 DNS/禁自动重定向、每钩子 4 次/每响应 1 MiB/阶段共 5 秒；请求/任务固定开关，离线试运行始终禁网 |
| Transform `reply` 联网 | 已支持的 HTTP 回复检查可单独授权，默认关闭；不继承 ingress/egress 开关，固定请求快照，复用受限 fetch/16 MiB 临时准入，单调用 5 秒/全部回复 hooks 累计 10 秒；所有试运行强制禁网 |
| Transform 原生 Anthropic Messages 请求阶段 | 已接 API Key、OAuth/setup-token、Vertex 和 Bedrock 的 request、账号/渠道范围晚 ingress、outbound 与只读 egress；实际 JSON 出站前执行，Bedrock 修改先于 SigV4 签名；原生渲染使用固定 RelayKit，保留业务 JSON；认证、speed、身份、厂商 envelope 受保护。全局/分组早入口不重复，原网关内部重试使用干净原生正文，原账本计费不变。透传晚 ingress/outbound 记 unchecked，egress 可拒绝；本地 web_search 模拟带尝试阶段时明确拒绝；原生 `@` 模型参数后缀已接通 |
| Transform Gemini / Antigravity 请求阶段 | 已接 Messages/Gemini 客户端 request、晚 ingress、outbound、只读 egress；Gemini API Key、OAuth（直连/项目包装）、Vertex 与 Antigravity OAuth 在真实 Gemini wire 上执行，厂商外壳、原生头、缓存引用及图片/候选等准入规格受保护。便携推理使用固定 RelayKit，回退按实际模型重新渲染，预算不冒充计费等级；物理重试重新处理干净正文，必选/审计失败不被内部重试吞掉。Antigravity 上游透传保持 Claude，只读 request/egress 与 unchecked 晚 ingress/outbound；原生 `@` 模型参数后缀已接通，countTokens 剥离修饰符但不开放生成控制，WS 未开放。详见 REQUEST_PROCESSING.md |
| Transform Messages / Gemini `reply/report` | 原生 Anthropic、Gemini 与 Antigravity 的 Messages / Gemini generateContent/streamGenerateContent 已接实际下发 JSON/SSE、实测用量与原账本确认后的报告；支持全局/分组和指定原生账号的 reply/report 单独或组合实例；reply 在原转换后的真实 JSON/SSE 上遮盖/停止，保留已采集用量，停止取消真实上游；失败/重复/旧无账本回退不伪造已确认费用；这些协议的 WS 尚未开放 |
| Transform 其他原生协议的出站、WS | 尚未接入；回复与完成报告不代表实际 Anthropic/Gemini 出站或晚 ingress 已接通 |
| `@` 模型修饰符 | Provider（Chat/Responses/Messages）与原生 HTTP 文本入口（OpenAI Chat/Responses/Messages、Anthropic Messages/Chat/Responses、Gemini HTTP/Messages、Antigravity Messages/Gemini）均已接 thinking/effort/temperature/topp、映射覆盖、scope/白名单、价格回退及本地 400 拒绝（不向任何上游发送 `@...`，Gemini 仅按最终显式 `thinkingLevel` 计费），使用共享 RelayKit parser/intent；显式请求插件覆盖可替换旧 budget。真实模型名 exact/RE2 豁免已接后台热编辑、管理员二次验证、数据库 CAS 和每节点两秒有界刷新；WS 仍待后续完成，见 `MODEL_MODIFIERS.md` |
| Multipart 上游文本字段 | 已接入，有界编码，按实际字节签名 |
| SSE submit / `submit-sse-delta@1` | 已接入有界事件聚合和 JSON state delta；必须明确完成，断流不制造成功 |
| Task 持久 fileRef | 已接受限私有素材存储、任务原子绑定、ctx.files、multipart fileRef、JSON 占位符与保留清理；`/v1/videos`、通用 `/v1/tasks/:key`（排除 `sunoapi`，其发送 multipart 返回 415 fail-closed）以及已声明 `meta.routes` 原生提交/根路径别名均已统一支持直接有界 multipart 暂存（最多 4 文件 / 32 MiB 单文件 / 129 MiB 总量 / 60s 截止 / 立即 unlink / 失败孤儿清理）与先上传 `file_references` 引用（含 user/Key/分组归属校验与只读请求插件投影）；官转五 1.1.0 已接专用持久上传/分片/完成/对象绑定与失败清理，详见 `FILE_HOST.md`、`OFFICIAL_UPLOADS.md` |
| New API Task 状态与产物查询 | `/v1/tasks/:key`、`/artifacts`、`/artifacts/:artifact_key/content` GET/HEAD 复用已保存插件任务，校验 user/Key/分组；列表禁网且无换票，内容只允许固定版本声明的产物。未迁移旧原生任务 |
| New API Task 按插件提交 | `POST /v1/tasks/:key`（key 为插件名）支持已绑定的视频 Task 驱动及内置原版 `sunoapi` 1.1.0（`suno_music` / `suno_lyrics`），支持 `fetchMode: "batch"`（`buildBatchQueryRequest` / `parseBatchResult`），产物经 `/v1/tasks/:key/artifacts` 公开下载，歌词经 `/v1/tasks/:key` 脱敏返回，与 `/v1/videos` 列表和读取隔离且不套用视频独立倍率。还支持有显式账号模型绑定的通用非视频 Task 驱动；合成分组不是此入口的普遍兼容承诺 |
| Task 取消与结果软删 | `POST /v1/tasks/:key/cancel` 或 `DELETE /v1/tasks/:key` 均校验 user/Key/分组。未提交排队任务可本地取消；已受理时仅使用固定版本的 `buildCancelRequest`/`parseCancelResponse`，HTTP 成功且明确确认才持久化取消并释放冻结；仅请求受理返回 202，未声明能力返回 409。`DELETE /v1/videos/:request_id`（返回 OpenAI `{"id":...,"object":"video.deleted","deleted":true}`，仅限视频协议）与 `DELETE /v1/tasks/:key/result`（返回 `204`）提供终态已结算任务的公开结果软删（迁移 `289_generation_public_result_deletion.sql`），活跃或未结算返回 409；软删后禁止公开查询、下载、取消与作为父任务派生，并拦截原幂等键重放（409），但完整保留历史账单、去重记录与任务审计日志 |
| Task JSON 二进制 `contentPath` | 已接入 GET/POST JSON 操作响应、32 段路径、标准/URL-safe Base64、GET/HEAD 交付；64 MiB 媒体上限，操作取回的重定向明确拒绝 |
| 模型广场 Responses WS 性能 | 已接多轮最终结果与 HTTP/WS 桥采样，按公开模型/原分组/目标平台隔离；写出失败、取消排除，隐藏重试不重复，复用现有有界 recorder 和实时失效通知；不是 WS Provider/Transform hooks 的接通 |
| Transform JSON MediaSource 与媒体效果 | 当前 JSON 内嵌附件已接 attachments/media/受限 UTF-8/UTF-16/PDF/DOCX mediaText、ingress/outbound media.text、HTTP outbound 图片重编码、宿主受限 fetch 流及摘要 UI；同请求多实例/阶段共用提取限额与有界缓存，文档只能读取，图片/文档解码先做并发和共享内存准入，子进程不接收二进制。Task JSON egress 的有界持久 fileRef 只读工具已接；客户端视频 multipart 及预上传元信息已接早/晚 ingress，Task 描述符 multipart 的 egress 接点及 Provider Images/Audio 文件见上表；其他协议的持久文件需逐项接入，见 `TRANSFORM_MEDIA.md` |
| 视频 `originTaskIds` | 已接现有 OpenAI Videos / 固定原生视频 decoder；先禁网候选解码、核对 user/Key/分组，再固定原账号/版本/连接/配置。接受事务重查父任务及连接并保存私有快照；Ark draft_task 用原上游 ID 提交；晚 ingress 不得增删或替换父任务。详见 `DERIVED_TASKS.md` |
| Sora remix | 两个标准 Videos 别名已接入；只接受 prompt，固定已成功父任务的公开模型、规格、账号和登录身份，先幂等重放再准入，复用持久队列/资金/任务日志；仅允许精确固定原版 Sora 源码，不把其他插件当作支持 remix。未知历史时长不猜测；真实厂商仍待验收，见 `DERIVED_TASKS.md` |
| Task Responses / Images | 已绑定声明的模型支持 `/v1/responses` 同步、后台和 SSE，以及 `GET /v1/responses/:response_id`；标准 Images JSON 和编辑 multipart 通过同一持久队列，处理产物和有界 Base64 回填。具体模式由脚本声明，等待接口只读取持久状态 |
| Task 原生声明路由与根路径别名 | `/v1/tasks/:plugin/native/...` 与静态路由未命中后的根路径别名中间件均支持已声明的提交（含 `POST`/`PUT`/`PATCH`/`DELETE`、方法区分幂等指纹与路径任务号/DELETE 强制 `originTaskIds`）、单条查询（含路径参数与查询参数如 `?task_id=`/`?file_id=`/`?task_ids=`）及动态批量查询，拦截系统保留命名空间与 `%` 编码路径，多插件路径冲突返回 409 并支持按模型消歧，完整经过鉴权、分组模型白名单、请求插件早入口与厂商多字段提示词审计；`:predictLongRunning` 仅在当前分组绑定声明插件时接管、未绑定组回退原生 Gemini 网关；`GET /v1/videos/:id` 仅对原生提交任务转交原生格式、保留标准 `/v1/videos` 响应；账号解绑或版本停用后仍通过任务固定版本只读渲染与投影历史任务产物。未绑定的旧原生视频不迁入 JS |
| `duration-auto@1`、`json-clone@1` | 按固定基线文档作为兼容 no-op 接受 |
| Task `json-order@1` | 已接 JSON decoder 输入、解码后的 requestBody、每次 driver 调用与提交/查询/内容描述符；按 JSON.stringify 保序、执行 toJSON、省略 undefined、不额外 HTML 转义，数字索引键仍按 JS 升序；有文件占位符的正文展开后排序。新任务原始解码正文持久保存，后台认领/重启读取保序，旧 JSONB 任务不补造丢失的顺序 |
| Task 描述符原始正文 | 字符串解码为插件自己的原始字节，null/未提供不发送正文；生成提交/查询的其余 JSON 沿用文件引用解析，fetch/内嵌产物下载保留插件编码的 JSON，不展开生成文件引用；三类请求均对最终实际字节签名，state/data 通过 RPC 保持 JSON 值 |
| 其他未知 required capabilities | 启用时拒绝，不静默忽略 |

request/outbound 按固定基线契约不提供 fetch；它们的禁网是原版约定，不是待补齐功能。

**因此本实现尚不能称为“完整兼容 New API 的全部插件能力”。**
标签 Task / Provider / Transform 表示脚本声明的能力，不能替代以上宿主验收表。
New API 的所有 Go 内置渠道也不等于这 17 个 JS 插件。

## 开发者模板与离线试运行

“插件开发”提供 Task 视频、Provider 文本和已接通的 `ingress` 三种模板，按当前用户命名空间生成源码。模板面向虚构上游，需按实际厂商文档修改地址、字段、状态与用量。默认模板不再使用尚未接通的 `request` 阶段。

`GET /api/v1/plugin-registry/templates` 返回模板和模拟输入；`POST /api/v1/plugin-registry/dry-run` 接受未保存源码、受支持的 hook 名和 JSON `args`。仅运行用户提供的数据，不读账号/配置/真实凭据，不安装或启用、不创建任务或访问账本，也不提供 fetch。按现有 4 个固定进程槽准入，单次最多 5 秒，输入参数总计/返回各最多 64 KiB，源码最多 1 MiB。每次使用全新子进程并关闭，避免读到或影响线上请求的 JS 全局变量。

报告返回源码 SHA256、耗时、hook JSON 输出和现有兼容提示。它仅检查函数执行和 JSON 返回，不执行全部宿主 effect 校验/网络/文件/资金流程，也不能视为真实上游验收；开发者可修改模拟响应检查解析钩子。运行结果随源码/模拟输入变化失效，关闭页面会取消。

## 下游接口

### 原生视频与任务日志

迁移 `258_native_video_task_history.sql` 将原生观察记录与插件任务合并为只读日志视图。管理员和普通用户继续使用各自的任务日志菜单、分页、详情和权限；`source` 区分 `native` / `plugin`，原生记录标识为 `native-xai_video` 或 `native-ark_video`，不伪造插件版本。

- 覆盖升级后经原生接口新提交的 Grok 生成、编辑、延长以及 Ark / Seedance 视频。原接口的请求、响应、账号选择和任务 ID 保留；新增 `native_video_…` 仅用于控制台日志。
- 接受提交后、向客户端返回之前尝试写入创建记录；随原生状态查询及 Grok 下载记录生成中、完成、失败/取消/过期。存储失败记系统告警，不重放已经接受的上游提交。
- 仅记录本站确认的新任务。升级前任务、未知状态和 HTTP/网络错误不凭空补建历史或推断失败；原生删除响应也不自动当作已取消。
- 同用户、Key、分组、账号、协议及上游 ID 唯一。重复、并发、乱序查询不会覆盖创建时的公开模型/参数，不会把终态改回进行中，也不会改写首次观察终态的时间。完成后允许补上后来出现的制品可用状态。
- 日志筛选支持粘贴原生接口实际返回的任务 ID（包括 Ark 不带 `seedance:` 的 ID），并保持用户归属过滤；不要求用户事先知道新增控制台 ID。
- 持久记录只保留公开字段与宿主生成的状态，不保存上游响应、签名 URL、Key 或插件 state。普通用户仍不返回上游账号/任务 ID。
- 原生记录不进入插件资金队列，不新增冻结、退款或第二套扣费。只有匹配原用户/Key/账号/分组的用量行，加上已提交的 `usage_billing_dedup`（或其归档）才显示已结算；记录零费用也能显示已结算。仅有用量行、扣费失败或尚未落账金额留空；有完整快照的新任务可显示待结算/待对账，简单模式和已确认失败的自动任务显示未扣费。
- 迁移 261 后，普通 API Key 新任务在创建时登记连接摘要，由固定后台 worker 仅 GET 状态，正常约 15 秒一次，支持数据库租约、重启恢复及账号共享并发。账号或连接异常、连续错误或超过 24 小时转“后台跟进待处理”，不伪造生成失败；前台确认终态也可关闭待处理。更早和 OAuth 任务不猜测回填，详见 `NATIVE_VIDEO_MONITORING.md`。
- 结束时间为本站首次观察终态的时间，耗时包含从提交到观察终态的等待；不是上游精确完成时间。日志页面刷新只读数据库，不触发上游请求；未登记后台跟进的任务停止客户端查询后可能保留旧状态。
- 能可靠解析价格的普通 API Key 新原生任务在提交前捕获私有计费快照，保存创建时价格/倍率、原订阅或余额及配额规则；完成查询/下载使用首次持久规则和原账本防重。Redis 丢失或被重复创建改写不重新定价；缺价、Grok Token 定价及 Ark 自定义分档采购统计仍保留原动态路径。没有预扣或旧价回填，详见 `NATIVE_VIDEO_BILLING_SNAPSHOT.md`。
- 原生成片继续经原接口查询/下载，控制台不显示插件下载按钮，也不把原生 ID 交给插件 content 驱动。缓存恢复和上述范围内的创建时价格/付款来源已接通；有完整创建价格/连接证据的新任务已接后台自动结算，首次用量不可覆盖，账本与用量事务提交；无完整证据的旧任务保留原路径。删除/派生执行、非线性快照和整体 JS Task 化仍待后续，见 `NATIVE_VIDEO_SETTLEMENT.md`。

### Provider 普通转发

在插件中心启用 `sub2api-openai-relay`，然后在 OpenAI 平台的普通 API Key 账号中选择该插件。上游根地址不带 `/v1`（路由会追加 `/v1/chat/completions` 或 `/v1/responses`）；配置公开模型到上游模型的显式映射，再配置分组和现有文本定价。插件无固定模型列表时，至少需要一个具体模型映射。自定义 Provider 可声明具体模型；纯正则或 catch-all 的动态模型需要显式账号映射，不能把通配符作为公开模型名。

新请求读取账号固定版本和启用状态；版本停用、配置/签名失败或模型不匹配均不会回退到原生协议。调度快照保留插件路由与模型事实，并按实际 HTTP 入口隔离；Messages、WebSocket、compact、Images 等未实现入口不会仅凭 Chat/Responses 能力选中这些账号。模型广场只纳入当前分组、账号模型和启用 Provider 支持范围的交集。

已支持的 relay 路由为 `/v1/chat/completions`、`/v1/responses`、`/v1/messages` 上的 `converter: none`、`converter: plugin`、受校验 RelayKit 预置及其 `+plugin` 组合（引擎底层 `protocolconv` 具备全部 12 条文本转换器），以及 `/v1/embeddings` 的 `none`/`plugin` JSON，不开放 `pass_through_body_enabled`。支持默认 Bearer、none/header/query 路由认证，`{model}` 与 `{config.<url-field>}` 目标；保留账号 base URL 路径前缀。插件转换和请求准备后的实际正文参与 SHA256 签名；所有普通 relay 钩子均不提供 fetch。只使用宿主、路由或插件显式生成的头与查询参数，不复制下游 Key 或客户端查询；准备阶段不能读取或改写认证头。目标变更后重新校验主机及 URL，受插件允许主机、服务端 URL 策略和公网 DNS/禁重定向传输约束；query 认证在最终目标确定后写入，避免改路径时丢失凭据。

`prepareRequest` 允许原子 JSON Patch 和业务 header patch，但不能改变基线规定的模型、流模式及计费字段，包括其大小写别名；有歧义的重复 JSON 键拒绝。`plugin` 转换支持有界 JSON 上游正文、JSON 响应、SSE 或 NDJSON 上游流转标准 SSE 或插件明确构造的 JSON，以及 `nativeResponse`。最终响应形态仍须与下游一致；支持 JSON next 握手后接最终 JSON/SSE。工具 hook 用量通过请求开始时固定的逐模型显式售价收费，支持明确免费零和同 key 协议计数覆盖；缺价时不套用其他厂商的默认价。不支持文件、WebSocket、非 JSON 二进制 chunks；JSON 客户端的流聚合不沿 nativeResponse 绕过转换。开发模板及准确边界见仓库根目录 `docs/customization/PROVIDER_PLUGINS.md`。

复用现有 OpenAI 响应解析、用量记录、账号并发、分组定价和倍率，没有第二套文本账本。配置校验和请求转换/准备各最多 5 秒，静态凭据交换最多 15 秒；单次初始化或换令牌后重建总预算最多 25 秒。每个响应转换调用另有 5 秒（含排队）。请求准备/转换和 JSON 响应上限 1 MiB，流的输入事件、输出批次和 state 各 1 MiB，最多 65536 次流钩子调用。每次执行后释放 worker，不占用上游等待或客户端读取的时间；等待上游流数据超过 30 秒会中止，客户端取消会关闭上游。仅干净 EOF 调用一次 `done: true`，传输错误不补完成帧。下游流要求标准 SSE，JSON 要求标准结果结构；HTTP 200 的错误正文、显式流错误、转换失败或终止前断流均不能记成成功，非 2xx 不允许转换成计费成功。文本走现有转发与账本；Images 和 Audio 的已绑定 Provider 入口使用各自专用处理器。平台原生“测试连接”和“同步模型”不绕过插件发起请求，需使用带分组 API Key 的实际协议调用验证。

普通 Provider 的 `checkKey/exchangeKey` 复用受限 HTTP 宿主，沿用账号代理、允许域名、公网 DNS 和禁重定向规则；其他 relay 钩子仍不提供 fetch。令牌不落库、不回传页面，按账号、固定版本/源码、Key、配置、地址和代理隔离；失败不缓存，不允许旧响应清除已替换的新令牌。单节点最多 10000 项缓存、128 个不同身份的在途交换；调用方取消不取消其他调用方共用的交换，该交换受独立 15 秒截止时间限制。新建或修改 Key/地址/配置/版本/代理时才校验，未改变连接身份的普通编辑不依赖上游在线。多节点分别缓存，重启后重新交换；已接上游余额、Provider 登录/刷新/撤销及定时维护；账号删除/解绑/换连接的旧授权通过持久队列清理。Chat/Responses 调度前凭据过滤已接入；原生永久停用事件及受限恢复已接入，视频 Task 已接同一 grant 生命周期及 Google oauth2_jwt；其他 Task 路由仍未齐备。详情和开发示例见 Provider 与 Task 文档。

### 请求处理入口

入口、Task egress 及开发示例见 `docs/customization/REQUEST_PROCESSING.md`（仓库根目录）。实例启停独立于 Task/Provider 总开关，按全局优先、分组随后及各自排序执行。只对匹配模型的入口生效；不支持的附件/非 JSON 请求在有匹配规则时明确拒绝，不静默绕过。每个 HTTP 请求固定实例和配置快照，上游重试不重跑入口。多节点配置缓存最长约 2 秒，刷新失败停止新请求。

单次入口总预算 5 秒，`utils.fetch` 需实例显式开启且受主机和资源限制，所有试运行强制离线；只接受原子 JSON Patch，不允许修改任何大小写或重复形式的 `model`、`stream` 及 session 中对应字段。配置密文保存；执行记录只保存动作、阶段、版本、耗时与计数，未成功写入记录时不向下游处理器继续转发。试运行使用未保存配置，不调用模型、不产生生成账单。

Task JSON egress 已接入冻结后、提交生成前的只读检查，全局优先于分组，包含 egress 的实例必须显式限于 task 格式。已接受任务保存加密配置/版本/源码摘要快照；拒绝、必选失败或审计失败不会发起生成，随后由持久结算释放冻结。客户端 `/v1/videos` 和 `/videos` multipart 早/晚 ingress 已接只读附件元信息和既有文本字段替换，保持原文件/幂等及资金链路；上游 multipart 描述符的文本与已绑定分片支持只读 egress；原始 opaque 文本仍标为 unchecked。Task report 已接最终结算/退款后的两固定 worker 和双上限队列，仅报告配置允许的主机；可丢且有计数，不影响账务。普通文本指定 Provider 账号的晚 ingress 及 request/outbound/egress/reply/report 已接通；请求阶段在实际尝试运行，reply 处理实际下发 JSON/SSE，report 等待实际账本结算。全局/分组 Chat/Responses、Embeddings 和 Images HTTP report 同样支持原生账号；全局/分组和原生账号的 reply 已接实际下游响应；原生 API Key 与 OAuth/setup-token Images JSON 及 `/v1/images/edits` multipart 请求阶段已接通（multipart 文本视图支持早/晚阶段提示词替换与晚阶段 `user`/业务头添加，二进制分片逐字节原样保留且不进入 JS 或 report，超过 1 MiB 上传图在 OAuth 路径通过独立投影处理），Responses 桥接的内部文本控制受保护；Grok Voice（`/v1/tts`、`/v1/stt`）、`/v1/web_search`、`/v1/x_search` 与 `/v1/alpha/search` 已接入唯一账本确认后的 `report` 上报（跳过早 ingress，二进制/multipart 请求与音频响应不泄露原始字节）；Responses WebSocket（`/v1/responses` WS，含 ctx_pool/shared/dedicated/http_bridge，以及命中插件或 `@` 修饰符时走受管轮次的 passthrough）现已按轮次接入六阶段（`ingress`/`request`/`outbound`/`egress`/`reply`/`report`）与逐轮 `@` 模型修饰符，单轮拒绝或非法修饰符仅发 `error` 事件不断开连接，reply `stop` 主动向原生 WS 上游发送 `response.cancel`（或在 HTTP 桥接立即取消上游 SSE）并按实收取消用量结算与上报确认账本 `report`。资源与流程见请求处理文档。

视频幂等身份取原始客户端请求的规范化 JSON 哈希，执行、报价与冻结仍使用处理后的请求。已接受的同键重试优先返回原任务，不因入口规则后来拦截或修改而重建任务；同键改变客户端正文仍返回 409。

### OpenAI Videos

使用本站 API Key，核心 OpenAI Videos 兼容路径：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/v1/videos` | 创建持久任务；支持 `Idempotency-Key` |
| GET | `/v1/videos/:id` | 查询归属于当前用户及 Key 的任务 |
| GET | `/v1/videos` | `after`、`limit`（1–100）、`order`（asc/desc） |
| GET / HEAD | `/v1/videos/:id/content` | 成片下载；支持 Range 和缓存验证请求头 |

- 当前核心接口支持 JSON 和标准 multipart。`input_reference` 可直接上传 PNG/JPEG/WebP，最多 32 MiB；本站多素材扩展允许最多 4 个媒体、总计 129 MiB，支持重复文件字段，文本字段保持唯一。后台使用私有持久快照，实际字段和数量由插件决定；原版 Sora 仍为单图。也可按 `FILE_HOST.md` 使用先上传/引用扩展或上游支持的 URL。官转五 1.1.0 支持宿主持久上传与对象绑定，见 `OFFICIAL_UPLOADS.md`；Task 描述符中的持久上传可供 egress 检查；其他协议的文件接点需逐项接入。
- 请求、状态和持久结果均有限额，主快照不超过 1 MiB。超过限制明确报错。
- 固定原版 Sora 的 remix 已另行接入，详见 `DERIVED_TASKS.md`；删除和其他编辑/延长没有自动变为通用兼容端点，旧内置协议仍保留其原有路径。
- 查询和下载不会再收取生成费用。余额不足后仍可查询已归属任务；禁用 Key/用户依然受权限约束。
- 同 Key 的幂等重试在重新选择渠道、读取当前售价之前查询持久任务；JSON 空白和对象字段顺序不影响指纹。相同键改变请求返回 409。已有任务在余额耗尽、插件停用或账号停止调度后仍可重试；新请求保持完整计费准入。
- 插件身份与声明模型进入调度缓存，缓存前后的渠道选择和原生协议隔离保持一致。
- 创建、列表、查询保留标准 `seconds`、`size`、`prompt` 字段及公共任务身份，不公开插件状态与上游凭据。
- 不依赖用户持续轮询，后台固定 worker 推进任务。
- SSE 上游提交最多 32 MiB / 4096 帧，单事件 1 MiB；30 秒空闲中止，服从操作总截止时间。当前不会向下游透传 SSE。
- 当前只持久化任务与产物元数据，成片通过上游代理下载；上游 URL 过期不能当成永久本地存储。
- `buildContentRequest.contentPath` 读取 200 JSON 长任务响应：error 为 502、未完成为 409、内容缺失为 410，完整校验 Base64 后流式解码交付。取回请求使用插件声明的 GET/POST 和实际 JSON 正文签名，不向上游 JSON 接口转发下游 Range/缓存验证头；HEAD 同样取回并核验，只返回媒体头。未生成媒体范围响应，不伪造 206；操作取回重定向暂时拒绝。
- 媒体最多 64 MiB，JSON 最多为该上限的 4/3 加 1 MiB；与基线一样，交付期间在 Go 内存保留 JSON。使用独立共享内存 scope 预留 JSON 上限的 5 倍加 1 MiB，并最多 2 个并发解码/交付；关闭、取消、错误释放，JS worker 在读取大正文前释放。下载依旧受账号并发和 5 分钟总预算约束。响应加上 sandbox CSP、nosniff 和 no-referrer。

## 定价与资金

- 账号可以按公开模型名或 `*` 配置规则；未配置时使用分组已有视频规格价。
- 支持固定单价、每单服务费、最低费用，以及管理员填写上游单价后的币种换算与加价倍率。另支持官转二/五的最终账单模式，按上游已结算的 `final_cost` 换算与加价。
- 线性规则单位为任务、秒、Token 或积分。另支持 New API Task 的受限多维表达式：`u("字段")`、`tier("档位", USD金额)`、条件和标量数学，按插件声明的 number / boolean / enum 用量字段计价。结果直接为 USD，不套普通 relay 的百万 Token 换算。
- 独立包 `newapi-engine/billingexpr` 已完整对齐基线 `pkg/billingexpr`、配额舍入与分档结算（含全部 13 项 token/图像变量、`tier()/fixed()`、`param()/header()` 敏感头拦截、分时函数与 AST `request_rules` 追踪）。
- Task 表达式支持 `u("字段")`、`tier("档位", USD金额)`、分时函数以及受理时冻结的字面量 `param("...")` 与非敏感 `header("...")` 探针，并记录命中的 `RequestRules`；敏感认证头（如 `Authorization`/`Cookie`/`X-Api-Key` 及含 `auth`/`token`/`secret`/`password`/`credential` 字段）与动态探针键、relay token 变量、`fixed()` 在 Task 场景一律 fail closed。
- 表达式必须填写正数最大基础冻结额；冻结规则、字段 schema、请求规格、时钟、探针与摘要，最终数字量只认独立完成事实；纯非数值枚举/布尔选择器在缺省终态回包时可安全复算，数值量绝不回退估价。数据库在资金事务中重新计算最终金额，超额或缺事实转人工对账。最大额在舍入前检查，零分组倍率仍单独结算账号额度。详见 `TASK_PRICING.md`。
- 支持 `hour/minute/weekday/month/day("时区")` 分时计价，使用固定基线的函数名与空/无效时区回退 UTC 规则，周日为 0。受理时固定时刻与时区 UTC 偏移，预估、终态和重启均不读取新的当前时间；缺快照拒绝自动结算。
- `cost_plus` 除支持管理员配置单价与最终账单模式外，现已支持插件可选导出售前报价扩展（`buildQuoteRequest`+`parseQuoteResponse` HTTP 报价或纯函数 `quotePrice`，原版插件零改动即零 I/O 跳过）；报价在 15 秒超时与既有 URL/代理/禁重定向约束下校验非负有界金额、币种、单位与过期时间，始终以 `Final=false` 冻结快照并在终态写入 `quote_comparison` 差价留痕，仅管理员任务日志详情可见 `billing` 摘要（普通用户严格脱敏）。
- `billing_pending` 超出等待窗口（默认 2 小时，可配置 `BillingPendingWindow`）时：固定按次价或纯非数值选择器表达式自动按冻结依据完成结算；依赖最终数值量或上游账单的任务转入 `BillingReview`（`upstream_billing_timeout`）人工核对。
- 新任务固定价格快照；随后改价不会重算旧任务。
- 创建事务同时记录任务、冻结用户资金并预记 Key / 订阅 / 账号额度。
- 资金结算、退款、事件和普通 `usage_logs` 在同一事务中提交。重复结算不重复收费。
- 计费按本站既有 8 位金额精度。零价必须显式配置，缺价不能自动成为免费。
- 最终用量与提交时的估计分开保存。缺少最终用量、超过已授权冻结金额时转人工对账，不自动追加扣款。渠道脚本声明账单待定时继续查询原任务，视频可先下载；生成终态满 2 小时仍无最终账单转人工核对。
- 持久生成中任务也受账号与用户并发限制；提交结果不确定的任务占用名额直到核实。
- 持久 JS Task 的 user × platform 额度及投递记录现与资金事务共同提交；Redis 以额度身份/cursor 去重，缓存回源、绝对快照 flusher、重试和进程重启保留 Task 费用。订阅、零费及无配置限额不新增正额度事件。不是普通同步调用和旧原生任务的全局耐久计量，也不改为平台限额的原子预留；范围、升级与回退要求见 `TASK_QUOTA.md`。

## 异常任务处理

独立“任务日志”支持用户归属查询、管理员分页筛选、时间线与鉴权下载；覆盖所有持久生成任务。“待处理”保留最早 100 条异常任务的对账工作台。管理员核查上游后可以：

1. 确认上游任务号并恢复查询。该操作不会重新发起生成。
2. 关闭任务并全额退款。
3. 对已完成但账单待核实的任务确认收费金额，不能高于原冻结额。

每项操作必须写明依据。活跃租约禁止人工抢改；已完成的任务不能重新打开轮询。原生任务在管理员详情另提供恢复查询及重试原账本结算，复用二次验证；固定原连接、规则和最终用量，状态恢复与私有审计同事务，不能修改金额或重新生成。详见 `NATIVE_VIDEO_SETTLEMENT.md`。
未确定是否被上游接受的提交不会换账号或自动重发，避免重复付费。

## 模型广场与监控

- 跨组去重，支持搜索、平台、分组、倍率、计费类型、声明协议，以及卡片/表格切换。
- 稳定详情地址为 `/model-plaza/models/mdl_…`，支持直接打开与刷新。
- 分组可见性和用户专属倍率先过滤，再返回详情与监控。隐藏和不存在均为 404。
- 上游成本、Key、密文配置不进入公共目录；价格页面只显示合成售价。
- 近 24 小时统计真实样本，视频使用创建到生成终态的完整耗时，含排队。
- 视频按生成终态计成功，交付状态独立展示；待完成任务不进入终态成功率。旧视频提交 HTTP 耗时不当作生成完成时长。
- 普通成功样本包括零费用日志；同请求的带内错误优先于部分用量记录。按请求去重，过滤可识别的业务拒绝和取消，持久任务的用量行不重复采样。Token/s 按有首字时间的总生成时间加权，不以完整耗时冒充生成速度。
- HTTP Chat / Responses / Messages 与 Gemini、Responses WebSocket 逐轮及 HTTP/WS 桥已接独立最终采样；关闭/删除用量与 Ops 日志不抹掉这些新样本。插件和旧原生视频均按持久终态统计。Images/Embeddings/Voice 已接实际 HTTP 结果，Realtime/Live 已接每轮明确终态；升级前历史仍用原日志；迁移切换和遥测过载/异常时可能缺样本。准确来源、资源/保留与非财务耐久边界见 `PERFORMANCE_METRICS.md`。
- 卡片展示逐小时调用状态；目录和详情每 15 秒刷新，隐藏页面暂停，返回时刷新。灰色表示无样本。当前分组/当前页已接受权限约束的短 SSE 更新通知，每两秒检查新独立样本及视频终态，15 秒轮询继续兜底；隐藏页面取消连接。资源与实际延迟见 `PERFORMANCE_METRICS.md`。
- 列表每页 24 个模型，当前页一次批量读取近 24 小时成功率和平均耗时；服务端按模型、平台和可见分组的精确组合查询，避免串组数据。
- 无样本、加载中和读取失败分别展示。独立详情默认进入“概览与能力”，按分组展示声明协议、输入/输出类型、推理、上下文和最大输出；普通模型取当前组全部可路由账号已保存目录的共同声明，数值取最小值，任一账号缺失声明即显示未知。只输出公开白名单，不暴露上游模型号、目录说明或工具配置。视频仅声明宿主已支持的输出类型，不根据模型名推测能力。
- Provider 的 Chat / Responses 示例仅按实际绑定且启用的路由显示。目录声明不等于真实调用验证；普通内置协议未建立可靠能力声明时仍显示未知。

## 本地验收与上线条件

本轮仅使用隔离的临时 PostgreSQL 做真实事务/迁移测试，未操作生产数据。
模拟上游测试覆盖原版 xai/doubao/sora 的提交、完成、用量、渲染和下载，以及签名、目标地址固定、错误不重复提交、SSE/delta、multipart 和 Ark endpoint 映射。

独立本地服务的真实 HTTP 验收已覆盖：创建、不同字段顺序重试、409 冲突、任务列表、查询、HEAD/GET 鉴权下载、后台结算。8 秒 × 0.02 USD 的测试任务仅向模拟上游提交一次，最终 `usage_logs` 一条、金额 0.16 USD。余额归零、账号停止调度及插件停用后的同键重试仍为 200，新请求为 403。这里的 HTTP 验收使用本地模拟上游，不代表真实 Sora 服务验收。

新增 `generation_http_local_test.go` 将通用视频插件的 HTTP handler、JS worker 子进程、后台任务服务与 PostgreSQL 迁移/资金事务一起验收。任务提交后重建服务、仓库和 JS 进程，随后修改账号默认地址与价格，旧任务仍使用原快照；请求 8 秒冻结 0.16 USD，最终返回 6 秒仅结算 0.12 USD，账本和结算事件各一条。覆盖同键重放/冲突、GET/HEAD/Range、用户归属及日志脱敏，读取日志不触发上游查询。确认失败且 `seconds:null` 自动退款；成功缺用量及 2xx 无任务号进入待核对且不自动重提，人工退款经原 worker/账本完成。

这项集成测试中的上游是仅映射固定测试域名的 localhost HTTP 服务；认证上下文、账号目录和并发缓存使用测试夹具，不能作为完整鉴权中间件、真实 Redis、管理页面或厂商协议的验收。可通过 `backend/scripts/test-video-plugins.sh` 重跑，脚本自建仅 Unix socket 的临时 PostgreSQL，运行 race 回归并清理。通用插件 `1.0.1` 对非法最终秒数保留任务终态，费用按成功待核对/失败退款处理；旧固定版本需显式升级账号，在途任务不迁移。

模型广场已做桌面 / 移动端浏览器验收，包括直接刷新详情、卡片 / 表格、价格、接口示例、平均/P95与分组数据。管理员页面受到现有法律确认弹窗限制，没有代用户确认；不能宣称管理员流程已完成浏览器验收。

真实 Key 的上游参数、用量与下载仍需正式联调；官转二/五已实现并有模拟回归，本轮没有发起付费生成。
完整插件兼容还需完成表中未开放的 Provider、Transform 后续阶段、文件与原生/派生任务接点。详细任务及当前里程碑见 `IMPLEMENTATION_PLAN.md`。
相关迁移包括 `253_newapi_plugin_versions.sql`、`254_generation_tasks.sql`、`256_plugin_registry.sql`、`257_request_plugin_instances.sql` 与 `289_generation_public_result_deletion.sql`，按现有启动迁移机制执行。
发布及部署结果以 GitHub Release 和实际部署记录为准。
