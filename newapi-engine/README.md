# New API 插件引擎

这是从本站已实现代码中提取的独立 Go 模块，使用固定 New API 基线
`48f9df96ea7c68eae04a0f3687869f232f48929f`。模块路径为
`github.com/Wei-Shaw/sub2api/newapi-engine`，当前通过主站 `replace` 引用同一仓库源码，尚未单独发布模块版本。

## 独立使用

```sh
cd newapi-engine
GOWORK=off go test -race ./...
GOWORK=off go build -o /tmp/pluginctl ./cmd/pluginctl
/tmp/pluginctl builtins
/tmp/pluginctl lint builtin/sub2api-openai-video/plugin.js
/tmp/pluginctl init task /tmp/my-video-plugin --key community-1-example
/tmp/pluginctl test /tmp/my-video-plugin/plugin.js --fixture /tmp/my-video-plugin/fixture.json
```

`pluginctl test plugin.js --fixture fixture.json` 沿用固定基线的离线回放契约。
`pluginctl worker` 提供与主站相同的 worker RPC；调用方持有进程生命周期，插件不能指定可执行文件。
这些工具不加载主站配置、数据库、账号或环境秘密，也不执行真实资金操作。
新建插件的规范与发布路径见 [PLUGIN_DEVELOPMENT.md](PLUGIN_DEVELOPMENT.md)，
接入另一宿主的端口与事务要求见 [HOST_ADAPTER.md](HOST_ADAPTER.md)。

## 本批边界

已提取 JS 契约/运行时、worker RPC/进程池、通用 Task hook 类型与校验、定价表达式、
参数/请求补丁、RelayKit 协议转换、系统脚本、模板及离线 CLI。原脚本字节、key、版本、
SHA 和 RPC 不因移动目录改变。来源与许可见 [NOTICE.md](NOTICE.md) 和 [LICENSE](LICENSE)。

`generation` 现已拥有持久任务/连接/文件/父任务/凭据绑定快照、固定价/成本加价/表达式
价格快照、后台执行状态机、提交/轮询/超时/待对账及结算决策，并提供可独立启停的
有界 worker。宿主通过 `Store`、`ResolveConnection`、`AfterSettlement` 接入：

```go
executor := generation.NewExecutor(generation.ExecutorOptions{
    Store: store, Resolve: resolveOriginalConnection, AfterSettlement: notify,
})
workers := executor.Start(ctx, 2)
defer workers.Stop()
```

`Store` 的认领必须包含租约隔离；过期的付费提交只能转待对账，不能重新提交。
`Settle` 必须在同一个短事务内重查终态事实、完成结算/退款、账本及额度 outbox；
通知回调不能代替耐久资金投递。受理与冻结仍使用主站现有同库事务，未拆开扣款。
独立测试包含提交后保存失败、结算失败后重启、最终用量缺失/超额、上传准备和取消关闭；
实际 PostgreSQL 与 HTTP 联合验收仍在主站单独运行。

`generation.ScriptDriver` 进一步提供不依赖主站的 Task 描述符构造、受理/查询解析、
最终用量/产物处理和状态投影；宿主仍负责原连接上下文、权限、签名和真实 I/O。
原版 Sora 可在 fake ports 下经历受理、重建脚本/执行器、查询、产物与结算意图的联合回归。
这不等于真实钱包或其他任务类型已验收。接口约束见 [HOST_ADAPTER.md](HOST_ADAPTER.md)。

`plugin` 提供安装/版本/启停/恢复与系统目录，`market` 提供来源、固定安装和社区投稿/审核编排，
`query` 提供只读任务日志投影与用户脱敏。宿主注入 repository、隔离运行时和能力评估，
领域错误由宿主映射为原 HTTP 错误；未提供业务能力评估的引擎默认拒绝启用插件。

主站通过 `backend/internal/newapihost` 单点装配运行时与持久任务执行器，
并按先排空任务、后关闭 JS 的顺序停止；同一 facade 不允许重复启动或停止后重启。
具体账号、凭据和 I/O 适配暂仍在 service，并非所有宿主实现都已搬进该目录。
`frontend/packages/newapi-ui` 提供注入 client/权限/翻译/Dialog 的独立核心组件，
市场、社区审核、源码和清理面板也已进入 UI 包，通过宿主注入的接口组合；原生 `.s2plugin` 页面单独管理。

受理/鉴权、账号凭据维护、具体协议执行/I/O、资金事务存储和
监控 **仍在主站**，会按已有方案分批提取。这不是完整引擎拆分已经完成。
回复过滤已提取为不依赖框架的 `replyfilter`，可直接接 `net/http.ResponseWriter`；`mediahost` 拥有媒体投影、私有文件只读检查、文本/PDF/DOCX 提取和图片重编码，通过 `WithMemoryAdmission` 注入宿主的共享请求预算。主站仍负责身份、执行预算、文件归属、真实 I/O、审计与原账本；官转五持久上传保留专用主站适配。
Sub 原生 `.s2plugin` 协议和运行时继续独立保留。

独立模块不得反向导入主站的 internal、账号、数据库、路由或原生插件包。
本模块的测试与主站 PostgreSQL / HTTP / 资金验收分别运行；仅通过模块测试不代表
真实上游、完整宿主兼容或财务集成已通过。
