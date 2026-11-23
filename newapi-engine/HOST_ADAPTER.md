# 宿主接入规范

本规范面向站点开发者。写渠道 JS 的作者参见仓库根 `docs/customization/VIDEO_PLUGINS.md`、`PROVIDER_PLUGINS.md`。当前固定 New API 基线为 `48f9df96ea7c68eae04a0f3687869f232f48929f`；以下端口不能理解为全部协议都已接通。

## 模块与依赖

模块路径为 `github.com/Wei-Shaw/sub2api/newapi-engine`，目前为同仓独立 Go module，通过主站 `replace` 引用。不要导入 Sub 的 internal 来运行 engine；也不要把原生 `.s2plugin` 运行时装进新引擎。当前生产入口是 `backend/internal/newapihost`，具体账号、文件、HTTP、密文、PG 和钱包适配仍有一部分留在 service/repository。

| 端口 | 必须由宿主保证 |
| --- | --- |
| `plugin.Repository` / `market.*Repository` | 不可变源码/摘要/版本，身份权限、命名空间、审核 revision 与并发 CAS |
| `plugin.Runtime` / `generation.ScriptHooks` | 有界子进程、超时取消、配置校验、同一固定源码；不能让插件选择进程或访问宿主环境 |
| `plugin.AssessHost` | 按实际路由/文件/凭据/计费能力评估；未提供时默认不可启用 |
| `generation.Store` | 租约 fencing、原子结算/退款、唯一账本和耐久额度 outbox |
| `generation.ConnectionRuntime` | 原账号/插件版本/连接/登录身份固定，并发、素材准备、签名及有界真实 I/O |
| `query.Repository` / `query.Access` | 只读查询，当前身份覆盖客户端过滤，私有信息脱敏；不授予任务写入权限 |

## 任务与资金事务

1. 入口先鉴权、核对模型/归属、解码及估价，生成固定连接、插件、凭据绑定、原始请求/文件和价格快照。
2. 在同一短数据库事务里受理任务并冻结资金/Key/订阅及账号额度；幂等同键同请求返回原记录，同键不同请求拒绝。不能先入队再另调一次扣款。
3. `Store.Claim` 将 queued 原子转 submitting，发放租约。提交过期且已越过付费边界转 `submission_uncertain`；不得再调用生成。素材准备是单独的、仍未提交的阶段。
4. `Executor` 在数据库事务之外调用连接；`SaveProgress` 拒绝过期租约，先持久终态事实。成片可用和账单可结算分开判断。
5. `Store.Settle` 必须重新读取锁定的终态/价格/实测用量，在同一事务里完成实际扣款、解冻/退款、用量日志、唯一结算事件和 quota outbox；不信任内存传来的修改。
6. `AfterSettlement` 只做尽力通知/缓存失效。它不是财务 outbox，不能用回调承担必须投递的扣款或配额变动。

这里没有分布式钱包保证。若改成远程数据库/服务，必须另做资金幂等、投递/回执/补偿/对账和崩溃演练。

## JS Task hook 的独立适配

`generation.ScriptDriver` 处理构造提交/查询描述符、解析受理/结果、终态用量和产物。它只依赖 `ScriptHooks` 和原任务上下文，不执行 HTTP，不读取账号/数据库，不直接扣钱。

```go
driver := generation.ScriptDriver{
    Hooks: generation.ScriptHooks{Call: callIsolatedHook, Has: hasExport},
    Context: originalTaskContext,
    OnResultURL: rememberPrivateReportURL, // 可选；不得公开上游 URL
}
descriptor, err := driver.BuildRequest(ctx, task, true)
// 宿主：校验 URL/方法/头/文件 → 权限和内存准入 → 签名 → 有界发送。
// 如果网络结果未知，返回 generation.SubmissionError{Uncertain: true}。
// 成功 HTTP 响应再交给 driver.AcceptSubmission；轮询交 AcceptQuery。
```

`ScriptResponse` 提供实际状态、经过过滤的响应头和有界 JSON 正文。不要把非 2xx 交给脚本改成成功。脚本已确认受理的上游 ID 即使立即完成 hook 失败也须保留，以便对账；查询 hook 链失败不应用半个终态。最终用量保留在 `_host_final_usage`，缺少数字不能回填估计。脚本未知状态进入审核，不猜测运行或完成。

当前 `ScriptDriver` 保留已支持视频路径的状态/交付语义；其独立化不自动增加图片、音乐、Responses 异步任务或动态原生路由。素材准备、Transform worker 释放再获取、SSE 聚合、真实签名/HTTP 和受保护下载仍由宿主适配。

## 回复过滤 HTTP 端口

`replyfilter.New(ctx, writer, format, instances, onStop)` 接受标准 `net/http.ResponseWriter`。每个 Instance 注入已授权、受限且类型化的 Call 回调；不能把账号秘密、宿主可变对象或任意网络执行器暴露给 JS。宿主在调用前做共享内存和实例数量准入，并在 handler 返回前调用 Finish 取得汇总记录，写回自己的审计。

过滤器保留 JSON/SSE、UTF-16 暂存/遮盖和实际客户端错误；不会补成功终态。onStop 和返回的取消上下文用于关闭真实上游，最终账务只能使用已有实测结果。标准 HTTP writer 由引擎适配；已有带 Status/Size 等记账方法的 writer 直接复用。可选 Hijack、Flush 和 Push 只转发底层实际支持的能力；net/http 调用者使用请求 Context 感知断开，不依赖已弃用的 CloseNotify。

当前公开入口仅为已接通的 HTTP 文本协议；WebSocket 与其他媒体协议接点仍须单独实现和验收。

## 媒体检查与内存端口

`mediahost` 不依赖 Sub/Gin，媒体字节和解码器保留在父进程。`New` / `NewWithCache`
接 JSON 内嵌附件；`NewWithFileReferences` 额外接仅能打开当前任务私有文件的 `task.OpenFile`。
文件授权必须由宿主完成，不能将任意 ID、磁盘路径或凭据交给核心去猜测归属。

```go
source, err := mediahost.NewWithCache(body, requestTextCache,
    mediahost.WithMemoryAdmission(func(ctx context.Context, bytes int64) (context.Context, func(), error) {
        // 在已有请求共享预算上开临时 scope，先预留，再返回该 scope 的释放函数。
        // 保留当前身份/取消；错误也不得留下已占用的内存额度。
        return reserveTemporaryWorkingMemory(ctx, bytes)
    }))
```

图片和文档解码前同时检查引擎的进程共享限额与宿主请求预算，成功、失败和取消均释放临时额度；
不返还调用前已有的原请求内存。未注入宿主回调时引擎的图片 512 MiB、文档 320 MiB 和各两槽
仍生效，但不代表已纳入宿主其他请求的总限额。宿主应将 `ErrMemoryCapacity` 归类为临时容量
不足，不能绕过预算后重试解码。

`TextCache` 只属于一个请求，可跨实例/阶段复用；不得跨用户共享。`PreparedEffects.Commit`
必须等宿主对完整补丁、保护字段和最终内存准入全部成功后再调用。持久 Task 文件仍只读，
multipart/任意文件 Transform 与真实网络读取没有因为代码提取而自动开放。

## 装配、查询与停止

只为同一任务表启动一个 engine worker 池；旧 native 历史跟进必须保持独立的 Claim/表归属。主站 facade 的 `StartTaskExecutor` 是一次性的，停止后不可重启。关闭时先取消并等待任务/主站维护，再关闭 JS pool，最后断开数据库/缓存。安装/市场动作不得修改原生插件生命周期。

本站的旧原生视频维护由独立 `NativeVideoWorkers` 装配，只接原生查询/结算存储、账号、网关和 Key。它不持有引擎或 JS pool；两套 worker 的上下文、关闭和排空互不影响。停掉 New API 不应阻止旧视频继续查询和原账本结算。这个后台边界测试不替代整站禁用/升级/回退演练。

任务日志通过 `query.Service` 读取；用户身份由服务端注入，禁止客户端传 user_id 就获得管理员视图。日志刷新只读数据库，不触发上游轮询或付费生成。模型目录/真实监控由主站按当前分组交集展示，未知样本继续保持未知。

前端使用 `frontend/packages/newapi-ui` 的纯 client/授权/翻译/Dialog/Pagination 端口；主站负责路由和服务端权限，组件默认只读。参见该包 README。

## 接入验收

```sh
cd newapi-engine
GOWORK=off go test -race ./...
GOWORK=off go test -race ./generation -run TestScriptDriver -count=1
```

独立回归使用未改源码的 Sora 和 fake Store/连接，覆盖受理后重建脚本/执行器、查询、产物、最终实测金额和公开脱敏；不会联网或执行真实资金。真实同库事务还必须单独执行仓库根 `backend/scripts/test-video-plugins.sh`。上线前仍需身份/权限、真实账号限额、迁移/回退和完整站点停用验收。
