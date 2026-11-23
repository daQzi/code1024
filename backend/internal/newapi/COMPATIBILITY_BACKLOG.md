# New API 剩余兼容工作

更新：2026-11-23。固定基线：`48f9df96ea7c68eae04a0f3687869f232f48929f`。

> **独立模块架构目标**：[独立 New API 插件与任务引擎](../../../docs/customization/NEWAPI_ENGINE_DECOUPLING.md)。原生 Sub 插件保持独立，新模块不整合其运行时；原 P0～P9 的修订见该方案第 7 节。本文保留 R1～R8 的兼容缺口分类。

当前状态是**部分宿主功能已对齐，完整兼容仍在实施**。下面的 8 个工作包各包含多个开发或验收任务，不能理解为只剩 8 个小修改，也不据此估算完成百分比。现状以 `HOST_COMPATIBILITY.md` 为准；历史实施与验证记录在 `IMPLEMENTATION_PLAN.md`。

## 当前优先级与验收目标

已完成现有系统的视频协议接入闭环，包含通用 OpenAI Videos 插件、账号具体模型边界、模拟上游与资金回归以及[视频开发规范](../../../docs/customization/VIDEO_PLUGINS.md)。运行时/契约、持久任务领域/价格、后台执行器、回复过滤和媒体检查/编解码核心已提取为独立 `newapi-engine` 模块；安装/市场/查询领域、独立插件中心/市场/审核/源码/清理/任务日志 UI（已验证独立构建与 Vitest）与 `newapihost` 单点装配（含 `newapi_engine.enabled` 开关与 `executor_adapter.go` 端口收口）均已接入。新增[插件脚手架与开发规范](../../../newapi-engine/PLUGIN_DEVELOPMENT.md)，旧原生视频 worker 现由主站独立启动/停止，不跟随 New API 生命周期；第 8 节停用/回退、双系统隔离及切换 worker 防重演练已在离线竞态与隔离 PostgreSQL 17 中通过。

2026-11-22 用户最新补充：独立建设 New API 插件与任务系统；现有 Sub 原生 `.s2plugin` 单独保留，不再迁入统一控制层。后续提取插件、Task 状态机/worker、市场与查询模块，再通过宿主适配接入本站账号、事务资金和公共页面。原版 JS **无需修改源码即可上传、安装并运行其适用声明功能**仍是验收目标。Task、Provider、Transform 的契约、宿主接点、配置、凭据、文件、计费和生命周期一起验收；未知或未实现能力明确报告，不能静默跳过后标记完全兼容。

官转二、官转五等渠道专项后置，系统完成后按公开开发文档作为社区插件独立开发/交付；不再以这些渠道的接口扩展替代通用系统开发。保留已有代码、固定版本和任务记录，不在本轮擅自删除、换 key 或迁移。其真实渠道联调不作为通用系统完成的前置条件；通用系统仍需原版插件、模拟上游和集成回归验证。

本轮已补媒体 HTTP 与 Realtime/Live 逐轮最终采样、Task `json-order@1`、原始正文/JSON 状态传递、Provider 上游流转客户端 JSON，以及当前 Chat/Responses 入口的五种 RelayKit 预置及组合转换。它们不自动接通以下功能。

| 工作包 | 仍需实现或验证 | 完成标准 |
| --- | --- | --- |
| R1 Provider 协议转换 | 独立引擎 `protocolconv` 已接入全部 12 条 RelayKit 文本转换器，Provider 宿主公开入口已覆盖 `/v1/chat/completions`、`/v1/responses`（含原生 Responses WebSocket 与上游插件 WebSocket）、`/v1/messages`（直通、预置与 `+plugin` 组合）、`/v1/embeddings`（`none`/`plugin` JSON）及 `/v1beta/models/*`（插件声明的 `generateContent` / `streamGenerateContent`，未声明与原生 Gemini 分组回退原网关），并已接入通用 tools hook、Anthropic/xAI/阿里 DashScope/GLM/Azure/Gemini 托管工具准入与防重计费及逐模型 USD 售价；真实上游联调随 R8 | 使用共享协议 codec；区分公开模型/上游模型/价格身份；JSON/SSE、WebSocket、错误、状态链、取消及原账本单次计费的端到端测试通过 |
| R2 Provider 凭据生命周期 | Provider 登录、续期、撤销、定时维护、删除/换连接清理、调度前过滤、原生永久停用事件/受限恢复及既有邮件通知已完成离线接入；视频 Task grant 也已共用该生命周期，真实厂商及通知投递验收随 R8 | 复用账号与密文存储；登录流程有归属与过期约束；有界并发/队列、多实例续期防重、撤销与重启恢复；原生 OAuth 不因插件登录改动而退化 |
| R3 通用与派生任务 | 通用非视频 Task、Responses 同步/后台/SSE 与取回、Images JSON/multipart、`meta.routes` 的本站命名空间与原生根路径别名（含路径冲突 409、系统保留路径拦截、鉴权/白名单/请求插件/提示词审计、`:predictLongRunning`、PUT/PATCH/DELETE 及账号解绑/版本停用后的历史查询）、已确认的上游取消，以及终态已结算任务/视频结果软删（`DELETE /v1/videos/:id`、`DELETE /v1/tasks/:key/result`、迁移 `289`，删除后不可下载/不可派生且完整保留历史账单与审计）已离线接入；已有 Sora remix、持久任务/产物和原版 Suno 批量查询保留；未绑定插件的 Grok/Ark 继续独立走原生；真实上游联调随 R8 | 各入口和历史行为逐项核对；固定账号/版本/归属；唯一 worker 和账本，缺证据保持未知 |
| R4 文件与媒体 | Provider Images/Audio（含 64 part 上限、共享内存准入、拒接压缩 `Content-Encoding` 415 及二进制音频 `nosniff`/`no-referrer`/`sandbox` 响应头）支持有界 multipart、raw file、`$file` 内联与后续请求，Provider Audio 已支持平价按次及按字数（TTS）/按实测时长（STT）分组计费（fail-closed）；`/v1/videos`、`/v1/tasks/:key`（通用非 Suno）、`meta.routes` 原生路由与根路径别名均已统一接入有界 multipart 暂存（129 MiB/4 文件/60s 截止/4 并发槽/立即 unlink/孤儿清理）与 JSON `file_references` 归属校验及只读请求插件投影，Task multipart fileRef 已接只读 egress；真实上游大文件与成片验收随 R8 | 素材归属、内存/流量上限、拒绝零出站、文件签名、重启及失败清理通过；渠道计量有明确证据 |
| R5 请求处理剩余接点 | Provider（Chat/Responses/Messages）、原生 HTTP 文本入口（OpenAI、Anthropic、Gemini、Antigravity）及 Responses WebSocket 按轮次的 `@` 模型修饰符、映射覆盖、价格/scope 回退和 exact/RE2 豁免后台热编辑均已离线接入；Embeddings 原生/Provider、原生 API Key 与 OAuth/setup-token Images JSON 及 `/v1/images/edits` multipart 请求阶段/report 已接（图片沿基线跳过文本 reply，二进制分片原样保留且支持超 1 MiB 上传），OAuth direct/Responses 实际晚阶段在换票前执行且回退重新处理；原生 Anthropic Messages（API Key、OAuth/setup-token、Vertex、Bedrock）、Gemini/Antigravity Messages/Gemini HTTP 及 Responses WebSocket（ctx_pool/shared/dedicated/http_bridge，以及带规则/修饰符时自动切换受管轮次的 passthrough）均已接六阶段与唯一账本确认 report（WS 按轮次隔离，早/晚拒绝发 `error` 帧不断连，reply stop 主动发 `response.cancel` 或取消上游 HTTP 桥接并结算取消用量）；Grok Voice（TTS/STT）、WebSearch/XSearch 与 AlphaSearch 已接唯一账本确认 report；真实上游联调随 R8 | 逐协议验证实际出站/下发内容；保持认证头、模型与计费约束；WS 按轮次隔离；真实模型名含 `@` 的豁免与范围匹配符合基线；未实现路径继续明确拒绝 |
| R6 价格与耐久配额 | 独立纯包 `newapi-engine/billingexpr` 已对齐基线全部 13 项 token/图像变量、`tier()/fixed()`、`param()/header()` 敏感头过滤、分时函数与 `request_rules` 规则追踪及舍入/结算语义；Task 表达式已补受理时冻结的字面量 `param("...")`/`header("...")` 探针、`RequestRules` 落库与纯非数值选择器无终态回包恢复；`cost_plus` 已接可选售前报价 hook（`buildQuoteRequest`+`parseQuoteResponse` / `quotePrice`）、过期与超限 fail-closed、终态 `quote_comparison` 差价留痕及仅管理员可见的 `BillingSummary`；`billing_pending` 超窗已区分确定性单价/纯选择器自动结算与数值/账单模式转人工审核；PostgreSQL/Redis 平台额度耐久 outbox 已接；真实渠道报价联调与生产额度升级/回退演练随 R8 | 不猜测上游价格；新任务固定价格依据；财务与 outbox 同事务，重复投递幂等；崩溃恢复、并发、补偿、原冻结上限、账单延迟与退款回归通过 |
| R7 其他协议最终监控 | 当前 Images、Embeddings、Voice、Realtime、Live 入口已接独立最终采样；真实渠道联调随 R8 验收 | 复用有界 recorder 与实时失效通知；成功/失败/取消/重试、首字与吞吐均按各协议真实证据；与 Ops/用量日志开关解耦；无样本保持未知 |
| R8 完整验收与交付 | 外部市场完整网络验收（GitHub 镜像和抽样源码已实测）；真实上游联调；管理员页面完整验收；迁移/回退、Release 与部署 | 8 位编号 resolve 与目录合并已接入；验证固定源码/摘要/撤回/权限；测试账号与限额内联调；升级与回滚演练；仅在完整开发和验收通过后声明对应版本兼容并发布，部署后复核业务链路 |

## 历史执行顺序（已由新计划接续）

下列顺序记录此前仅补 JS 宿主缺口时的安排。当前先按独立引擎方案完成边界保护、插件与 Task 提取、管理入口分离，再补以下宿主缺口；不再执行双运行时统一改造。

1. R1：继续补普通 Provider 的其他公开协议和工具费用；R2 的 Provider 账号生命周期离线开发已接通，真实联调随 R8。
2. R3 与 R4：补其他任务、文件/媒体接点和视频高级操作；官转五 1.1.0 持久上传已完成离线实现，真实存储验收随 R8。
3. R5：补剩余请求处理协议、WebSocket 与模型修饰符。
4. R6 与 R7：R6 离线计价表达式、可选售前报价与差价/恢复逻辑已完成；真实协议监控联调及生产额度升级/回退演练随 R8。
5. R8：原版插件导入/执行、市场/页面验收和迁移演练后交付通用系统；官转二/五等独立渠道的开发、联调及社区发布随后进行。

开发中继续使用隔离数据库和模拟上游。真实联调需要能使用目标渠道/模型的测试账号、明确测试额度与素材权限；这些资料不阻止先完成其余离线开发。已有的生产 API Key 不写进源码、测试夹具或提交。

## 剩余任务归属（2026-11-23）

通用 OpenAI Videos、JS 源码安装/绑定、系统与社区市场、任务日志、分组模型广场和指标已实现。以上能力与全部固定基线入口的逐项兼容是不同验收范围。

| 归属 | 尚未处理完的内容 |
| --- | --- |
| 编码（第一批离线任务） | 已全部完成：未提交收尾、R4 文件/媒体尾项、模块解耦及第 8 节演练、官转二/五独立社区插件拆分均已实现并通过离线/隔离 PG 17 验收 |
| 真实验收（用户负责） | 测试账号/额度、厂商登录续期、真实价格及扣费、渠道素材与成片、外部市场目录/编号/撤回、管理员页面和分组模型/指标刷新 |
| 上线验收（用户负责） | 数据升级和回退（含 `253`/`254`/`256`/`257`/`289`）、推送、Release、启幕部署、上线后业务与账本复核 |

## 对“与 New API 一致”的口径

- 已移植其固定版本的 JS 契约、运行时与 17 个原版 JS 系统脚本，并用本站账号、任务、资金、前端与监控实现宿主接点；另有 4 个本站脚本。
- 已能使用的具体 hooks、格式、作用域和限制，逐项列在兼容矩阵。脚本能安装或编译不代表它调用的宿主功能全部可用。
- New API 的全部 Go 内置渠道和所有未来社区插件不包含在“21 个系统脚本”这个数字里。支持一个渠道必须同时验证协议、凭据、文件、费用和交付。
- 完成上述工作后仍须按固定基线逐项验收；版本升级另做差异评估。当前不得对外标记“New API 全功能完全兼容”。

市场增量已核对固定基线：code resolve 指 8 位社区编号查找。本轮补齐编号入口与目录条目合并；同源绝对和相对源码 URL 下载及固定摘要检查已具备。任意 URL 查询代理、自动合并 JS 源码不属于该基线已有能力，不列为兼容的必需任务。
