# New API 独立插件与任务引擎：架构修订

日期：2026-11-22。状态：**已提取运行时/契约、持久任务领域/价格与后台执行器；安装/市场/日志查询领域和 HTTP 管理入口也已分离；受理、具体宿主协议及 UI 接入继续迁移。**

实施顺序补充：已先利用现有 Task 引擎交付视频协议接入闭环，新增 `sub2api-openai-video`、具体模型约束与开发规范，见 [VIDEO_PLUGINS.md](VIDEO_PLUGINS.md)。现已创建 `newapi-engine` 独立模块并接回主站构建，准确边界见第 7.1 节；完整引擎迁移仍分批推进。

## 1. 本次调整

用户补充：现有 Sub 插件单独保留，不再改造并入另一套插件系统；独立建设基于 New API 的插件系统和任务系统。

因此建议从上一版“统一控制层、双运行时”调整为：

**Sub 原生插件保持原样；New API 插件、任务与市场独立成模块；主站通过适配层连接账号、资金、权限和页面。**

两套系统分别管理插件安装、配置、启停和版本，不互相接管运行时。New API 模块只需要理解 New API 的 JS 契约；不需要识别 `.s2plugin`、原生 gRPC 或 UI Bridge。

这里的“独立”先指源码、模块依赖、业务职责、数据所有权、测试和版本独立。第一阶段仍可编译在同一主站进程中、使用同一 PostgreSQL，便于保留现有任务与资金的事务一致性。独立进程或独立服务是后续部署选项，需要另做资金和任务的一致性设计。

## 2. 为什么建议这样拆

| 方案 | 对现有实现的影响 | 适用判断 |
| --- | --- | --- |
| 继续统一两种插件运行时 | 需要改旧插件管理、身份、权限和生命周期；通用接口还要兼顾两种不同契约 | 本次不继续推进 |
| **独立 New API 引擎，同站集成** | 提取之前新增的 JS、任务和市场代码；旧 Sub 插件保持；通过明确接口接主站 | **建议先采用，可复用之前的开发** |
| 直接拆独立服务/直接使用完整 New API 站点 | 可以获得进程隔离，但账号、用户、额度、任务 ID 和财务同步成为跨服务问题 | 有单独运营或部署要求时再评估；不能当作移动目录即可完成 |

与之前只提取 JS runtime 的设想相比，这次必须把**任务状态机、队列编排、轮询、产物、价格快照、任务日志查询和插件管理编排**一起纳入独立模块的职责。否则只是换目录，`GenerationService` 仍依赖 Sub 主站，独立不了。

源码基线仍为 New API `48f9df96ea7c68eae04a0f3687869f232f48929f`。该版本的 `service/task_polling.go`、`service/task_billing.go` 和 `model/task.go` 直接依赖其 common/model/relay/setting 等宿主包，因此插件与 Task 系统还需要做模块提取和端口适配，不能把整套 Task 直接当现成独立 SDK 引入。其 `relaykit/go.mod` 的独立模块方式可以作为组织参考；任务语义、契约和行为继续按固定版本对照。

## 3. 当前代码里的真实耦合

以下为本轮核实到的主要落点：

| 当前位置 | 耦合 | 提取方式 |
| --- | --- | --- |
| `newapi-engine/` 与 `backend/internal/newapi/` | 通用契约/运行时、任务、Provider 工具价格、插件/市场/查询、回复过滤及媒体检查/重编码已提取；共享请求预算与渠道持久上传由主站适配 | 后续收口具体账号/I/O/传输编排；渠道实现与通用 core 分开 |
| `backend/internal/service/newapi_plugin.go` | 直接持有 gateway、proxy repo、files、preparations、settings、密文及凭据维护等 | 拆成模块内插件服务与主站适配，移除对具体 Sub service 的依赖 |
| `backend/internal/service/generation_service.go` | 直接持有 AccountRepository、OpenAIGatewayService、BillingCacheService、APIKeyService | 任务状态机和 worker 进入 engine；账号、财务和缓存接外部接口 |
| 同一 `GenerationService` 构造函数 | 同时启动新 Task worker、旧原生视频跟进和结算 worker | 分开生命周期和认领范围；旧原生视频兼容跟进留主站；不能两套 worker 都执行同一任务 |
| `generation_task.go` / `generation_task_repo.go` | Task 类型定义在 service；repo 方法接受 `UsageBillingCommand`，SQL 映射直接依赖 service 类型 | Task 类型移到 engine；保留 PG/资金事务的主站适配；用稳定命令/结果类型过渡 |
| `backend/internal/handler/admin/plugin_handler.go` | 同一 handler 持有 PluginManager、NewAPIPluginService 和 GenerationService | 迁出新增 New API 管理接口；旧 Sub handler 保持原职责和协议 |
| `frontend/src/views/admin/PluginsView.vue` | 同时读取 JS 与 native 列表、混合展示和跳转 | 分成 New API 插件页面和原生扩展页面；保留旧组件行为 |
| `backend/cmd/server/wire*.go` | 主站负责所有新增服务和 worker 的装配 | 由单个 engine 集成入口装配，主站只提供宿主 ports 和启停 |

这些属于源码提取和边界改造，不需要把已有任务、费用记录和市场功能删掉重写。

## 4. 模块怎么组织

以下为最终目标目录，**尚未全部创建**。当前模块路径固定为 `github.com/Wei-Shaw/sub2api/newapi-engine`；此路径是仓库内独立 Go 子模块，尚未单独发布。当前实际包列表与构建方式见第 7.1 节；不要把目标树当作已完成清单。

```text
sub2api/
├── newapi-engine/                      # 独立 Go module
│   ├── contract/                       # 固定 New API API v1、schema、类型、兼容矩阵
│   ├── plugin/                         # JS 安装、版本、配置、启停、能力判断
│   ├── runtime/                        # JS worker/资源限制
│   ├── provider/                       # Provider、凭据生命周期、协议转换
│   ├── transform/                      # 请求处理、reply、report
│   ├── task/                           # 目标：状态机、幂等、调度、租约、查询/派生、恢复
│   ├── artifact/                       # 文件/产物的通用规则和访问契约
│   ├── pricing/                        # 表达式、用量、成本/售价、快照与结算意图
│   ├── market/                         # New API 系统/社区市场、投稿审核
│   ├── query/                          # 任务日志、执行记录和能力目录的查询模型
│   ├── ports/                          # 身份、连接、存储、事务、文件、I/O、指标接口
│   ├── testkit/                        # mock 宿主、原版脚本、协议与故障夹具
│   └── cmd/pluginctl/                  # 独立校验与模拟执行工具
├── backend/internal/newapihost/        # Sub 对 engine 的唯一集成适配边界
├── backend/internal/service/          # 原有业务和旧 Sub 插件继续保留
├── backend/pkg/pluginapi/              # 原 .s2plugin 协议保持原样
└── frontend/packages/newapi-ui/        # 可单独构建的 Vue 组件，client/权限等注入
```

原则是：`newapihost → newapi-engine`，引擎不能反向 import Sub 的 service/repository/handler；引擎和旧 Sub 插件系统之间没有运行时依赖。

独立模块提供启动、停止和服务接口；Gin 路由、Sub 数据库装配和账号页面由宿主适配。CLI/testkit 可以使用 mock ports 独立运行。它们的通过不代替生产 PG 资金事务验收。

## 5. 谁负责什么

| 领域 | New API 引擎 | Sub 主站 |
| --- | --- | --- |
| 原生 `.s2plugin` | 不导入、不代理、不升级 | 原 PluginManager/runtime/proto/签名/UI Bridge/HostService 继续管理 |
| New API 插件 | 安装/版本/能力/运行/配置/市场与审核编排 | 管理员/作者身份与权限、持久化和加密适配 |
| 上游连接 | 定义协议、模型、插件绑定、连接快照和健康/凭据维护规则 | 现有账号页面提供新连接入口；通过 adapter 提供有权使用的连接、代理和秘密引用 |
| Task | 拥有任务领域模型、状态机、幂等规则、worker、租约、轮询、父子任务与恢复 | 实现事务存储、权限入口和必要的历史适配 |
| 文件/产物 | 拥有生命周期和操作规则、任务绑定、结果描述 | 实现私有文件存储、有界读取和共享 I/O/内存预算 |
| 价格 | 拥有 New API 计算语义、用量、表达式/报价快照与结算意图 | 提供本站生效价格/分组/付款来源规则，并执行实际余额变动 |
| 钱包和额度 | 通过 port 请求原子预留、结算、退款，并持久记录对应事实 | 钱包、订阅、Key/账号额度、资金流水与缓存投递适配；只保留一套真实账本 |
| 日志 | 提供自己的 Task 查询模型和事件；不再依赖 Sub 的视频 DTO | 独立“任务日志”入口、访问控制；旧视频历史可通过只读适配显示 |
| 监控 | 输出有界的真实终态事实和稳定去重标识 | 现有 recorder/聚合/分组权限/模型广场通知；防止与旧监控重复采样 |

**任务系统独立，不等于再开一个用户钱包。** 引擎决定这一单处于什么状态、按什么快照算钱；宿主执行一次可审计、可幂等的资金操作。

### 5.1 账号入口可以继续复用

用户仍从“账号 → 添加连接”进入。页面提供 New API 插件连接表单，配置交给 New API 模块；旧 Sub 插件绑定、OAuth 和原生传输保持自己的流程。

引擎只获得本次有权使用的连接投影/不透明引用，不读取整张账号表、不直接调用旧 PluginManager。公共账号壳、分组和代理可以复用；两套插件的私有配置、版本选择和凭据维护不能互相改写。

### 5.2 独立数据所有权与兼容历史

模块拥有插件/版本/实例、任务/事件、文件绑定和市场记录的领域定义及迁移契约；实际执行迁移由部署宿主协调。第一阶段可以保留现有物理表名，通过 repository adapter 映射，避免同时改架构和搬迁大批数据。

不要为同一个在途任务新建另一条任务事实。旧任务仍沿用原 ID、原插件版本/摘要、原价格/连接/凭据快照和已确认账务。认领路径必须确定任务执行归属；先确保旧 worker 不再认领转交给 engine 的任务，才能启动新 worker。旧类型/格式不被新引擎猜测性执行。

旧 Sub 原生任务日志和 New API 任务日志是不同来源；如需一个页面查看，可在主站做只读查询聚合，明确来源与 ID 归属。查询聚合不能接管提交/结算，也不能把一份任务再写成两份。

### 5.3 金融事务边界

第一阶段仍使用同一 PostgreSQL，由主站提供明确的事务存储接口，把“任务受理与额度冻结”“最终事实与结算/退款/资金流水/已有额度 outbox”分别在必要的短事务内原子提交。

不能先调用普通 TaskStore.Save，再另调钱包扣款，就声称两个成功或失败一定一致；也不能在上游网络请求期间一直占用数据库事务。未知提交仍转对账，禁止换系统或账号自动重发付费请求。

如果以后引擎独立成远程服务或独立数据库，这个同库事务不再成立。届时需另外实现资金操作幂等键、耐久投递、回执/补偿/对账与故障演练，再开放远程模式；不能把本次 ports 自动称作已支持分布式财务。

## 6. 之前的改造怎么保留

| 已有成果 | 建议处理 |
| --- | --- |
| JS runtime/契约/codec/请求与回复工具 | 直接迁入新模块，保持固定基线与来源记录 |
| Provider/Transform/凭据维护 | 通用编排迁入；gateway、账号、密文、代理和通知通过 ports 接入 |
| 持久 Task/租约/幂等/文件/恢复 | 领域和 worker 迁入；PG 事务/文件存储留 adapter；不清空旧数据 |
| 冻结/结算/退款/对账、quota outbox | 复用现有资金链，提取稳定事务命令和结果；不会因为换包就重新扣费 |
| 插件市场/审核/版本/SHA/源码 diff | New API 部分迁入其独立控制层；旧 native 市场/安装能力保留原路径 |
| 当前混合插件页面/handler | 迁出新增 New API 部分到独立入口；保留旧 native 的逻辑和接口 |
| 模型广场/真实性能采样/通知 | 仍是本站公共产品；通过 New API 能力目录和终态事件 adapter 接入 |
| 旧原生视频跟进/结算 worker | 留在主站兼容层，分离启动/认领范围；不随通用引擎强制迁移全部旧视频执行 |
| 官转二/五已有专项 | 保留原绑定/快照；专项文件与通用引擎隔离，系统完成后按 SDK 整理社区插件 |

“旧 Sub 插件不改”保护的是其协议、运行时、能力和数据。为拆走之前添加的 New API 页面/handler/装配代码，可以修改公共装配点；不得借机重写旧插件业务，也不能把整个旧文件回退而丢掉无关修复。

## 7. 迁移顺序与原 50 项任务的调整

开发者入口：[独立插件开发规范](../../newapi-engine/PLUGIN_DEVELOPMENT.md)提供三类插件的脚手架、离线 fixture、投稿和升级流程；[宿主适配规范](../../newapi-engine/HOST_ADAPTER.md)说明其他站点如何接入账号、文件、任务事务和生命周期。两者分别面向渠道作者和宿主开发者，不要求渠道作者复制本站业务代码。

沿用原 P0～P9 阶段编号，**与独立引擎边界冲突的旧任务描述由本表替代**；其余兼容性、资金安全和验收要求保留。先执行下面前三批结构工作，再继续补协议功能。

| 阶段 | 原任务如何调整 | 本阶段出口 |
| --- | --- | --- |
| P0 基线和边界 | P0-01～03 保留原版契约/夹具；P0-04 改为两套独立系统边界与入口归属；P0-05 增加 worker/任务数据归属清单 | 有模块所有权和迁移保护清单，不再设计双运行时统一 Driver |
| P1 模块提取 | P1-01/02 改用 newapi-engine；P1-03 改为 Task/worker/插件控制层提取，取消 native runtime 提取；P1-04 为 New API 独立 UI；P1-05 覆盖独立模块与宿主构建 | 有 fake host 可跑的 engine，主站通过 newapihost 集成；旧插件不依赖 engine |
| P2 控制层和市场 | P2-01 改为 JS/sidecar 导入；P2-02/03 只管理 New API 版本/兼容；P2-04/05 只交付 New API 市场/投稿审核 | New API 页面、接口与存储逻辑独立；旧 `.s2plugin` 保留原安装入口 |
| P3 Provider/账号 | 原协议补齐任务保留；P3-04 只提取 New API 凭据生命周期，P3-05 经连接适配集成账号 | 不直接引用 Sub gateway/AccountRepository，不改变旧 OAuth 传输规则 |
| P4 通用 Task | 原通用模型/协议/batch/动作保留；P4-01 状态机与调度归 engine；P4-05 收缩为历史兼容和显式新连接接入，取消强制接管全部旧执行 | 引擎可以独立调度/恢复，旧任务不被两边同时认领 |
| P5 文件/Transform | 原接点补齐保留；真实 I/O、共享预算由 ports 注入 | 引擎无 Sub internal 依赖；晚 ingress、WS、媒体和终态规则不退化 |
| P6 价格和账务 | 原表达式/快照/报价/UI 保留；实际资金操作通过原子事务 port，复用 outbox | 只有一套账本；任务领域独立与资金一致性同时通过验收 |
| P7 日志/监控/模型 | 日志查询归 engine；模型广场/聚合/通知仍由主站整合；取消 native 管理统一 | 独立任务菜单，按分组的模型/价格/指标正确，旧来源不重复统计 |
| P8 开发规范 | 面向独立 New API 引擎发布契约/SDK/CLI；P8-03 改为宿主 adapter 与事务接入示例，取消重建 native SDK 的要求 | 新渠道作者只需开发 JS；宿主开发者可照文档接另一个站点 |
| P9 验收交付 | 原版逐能力、PG/权限/迁移回退/UI 验收保留；双运行时集成验收改为两套系统互不依赖的验收 | 独立版本/构建/停用验证与任务/资金故障演练通过 |

### 前三批提交建议

1. **边界与保护用例**：固定所有权、接口和不可改动的旧行为；记录现有任务/worker/资金接点。本批不搬数据、不切流量。
2. **核心与 Task 提取**：先迁纯 runtime/契约，再迁 Task 领域与 worker；主站 facade/adapter 先保持现有调用方式和物理表名。按可验证调用链分批提交。
3. **管理界面和接入收口**：拆 New API handler/UI，主站单点装配引擎；随后才继续原 Provider/Task/文件/定价等兼容补齐。

每批做到：明确只修改哪些新增功能、构建/测试结果、旧任务如何认领、是否切换新任务、如何回退；本地提交和 push/Release/部署分别记录。

### 7.1 已实施：纯运行时与契约独立模块

- 实际目录为仓库根 `newapi-engine/`，有独立 `go.mod/go.sum`、AGPL 来源/许可及 `cmd/pluginctl`。已迁出 `builtin/cli/codec/contract/contracttypes/jsplugin/limits/logbridge/modelmodifier/paramoverride/protocolconv/requestpatch/runner/task/taskprice/templates` 共 16 个包。
- 主站通过 `backend/go.mod` 的本地 `replace` 引用，不依赖 `go.work`。模块在 `GOWORK=off` 下可以独立测试、构建和执行插件校验。模块源码含测试、工具及带构建标记的文件均受反向依赖检查保护，不能导入 Sub 主站包或 Gin；路由参数使用小接口接入，主站 HTTP 写入器留在宿主。
- 第一批提交时的 `task` 包仅为 JS Task hooks 的 DTO/校验；后续持久任务提取见第 7.2 节。该批次中`GenerationTask`、后台任务认领/调度、原生视频跟进、插件管理/市场、账号、文件存储、钱包事务、quota outbox、监控和 UI 均沿用当前主站实现；`newapihost` 单点装配尚未建立。
- 21 个系统脚本源码字节、版本和 SHA 不变；原账号固定 revision、任务 ID、价格/连接/凭据快照及物理表均不变。不增加迁移、不启动第二套持久任务 worker、不复制任务或资金事实。当前业务测试继续验证原受理/冻结/结算/退款事务。
- 此阶段媒体共享预算、Gin writer 和专用上传暂留主站；后续回复过滤和媒体核心分别已继续提取，见第 7.10、7.11 节。专用上传仍由主站适配。
- 已接入独立 CI 测试/CLI 构建、主站 Makefile、三个源码 Dockerfile 和视频资金验收脚本。三个源码镜像都以仓库根为构建上下文；后端开发镜像改用 `docker build -f backend/Dockerfile .`，不能再只发送 `backend/` 目录。GoReleaser 的仓库 checkout 构建继续通过主站 local replace 找到同版本源码。
- 回退方式为同时回退本批模块目录、导入、go.mod 和构建入口；无需回滚数据库或改写任务。仅删除模块目录或只回退 go.mod 会使源码构建失败，不能作为回退流程。

### 7.2 已实施：持久任务领域、价格和后台执行器

- `newapi-engine/generation` 拥有原持久任务及连接/凭据/文件/父任务投影、公开视频响应、价格规则和不可变快照、执行状态机、终态结算决策及独立 worker。主站使用类型别名和薄 facade，原 JSON、任务 ID、原表、历史版本/连接/价格快照保持不变。
- `Store` 端口直接复用现有 PostgreSQL 的租约认领、进度保存和原子结算；没有把资金操作拆为普通保存加异步扣款。受理与冻结仍在原 `CreateHeld` 事务中。实际钱包、订阅、Key/账号额度及 outbox 不迁入第二套账本。
- 主站 `generation_engine_host.go` 将账号/并发/具体插件执行和渠道上传错误适配为小接口；引擎不引用 Sub/Gin 或官转上传包。此文件暂留 service，后续再收口到 `newapihost`；不能把它描述成唯一宿主边界已完成。
- 原 `GenerationService` 不再实现后台付费任务状态机；它委托唯一的 engine worker 池。文件/上传/outbox 维护在 `generation_lifecycle.go` 启动；旧 native poll/settlement 后续已独立到主站的 `NativeVideoWorkers`（第 7.8 节），不新增第二条任务事实或并行付费提交路径。
- 提交后持久化失败仍依靠租约转 `submission_uncertain`；重启只恢复终态结算，不重新生成。上传准备仍在付费边界之前，成片和账单分开判断；未观察到最终用量或超过冻结上限继续转审核。
- 独立模块可用 fake ports 测试状态机与关闭恢复；主站隔离 PostgreSQL/HTTP/真实 JS 子进程验收另外验证同库资金事务。这些测试不等于真实厂商调用或完整 New API 全功能兼容。

接下来收口 `newapihost`、插件控制/市场和管理入口，再继续剩余协议能力。最终完成标准仍按第 8 节逐项验收。

### 7.3 已实施：插件控制、市场和任务日志查询领域

- `newapi-engine/plugin` 拥有版本/能力 DTO、系统脚本目录、安装/摘要和身份检查、启停、版本恢复、系统版本登记和源码只读校验。宿主注入隔离运行时、版本存储及实际能力评估；缺少能力评估时默认不可启用，不能把 runtime 支持误当作业务宿主已接通。
- `newapi-engine/market` 拥有外部市场来源/固定安装依据，以及本站社区投稿/命名空间/所有权/不可变发布版本/审核与安装编排。实际保存与状态切换继续通过原 PostgreSQL repository 执行；原权限、作者、revision、SHA 和已发布历史保留。
- `newapi-engine/query` 拥有任务日志查询投影和用户可见性规则；只接查询存储，没有提交、认领或资金权限。宿主仍可只读合并 native 历史并补充管理员维护动作；不能借查询聚合重建任务。公开投影使用副本，避免脱敏操作修改共享管理员数据。
- 原 `PluginHandler` 现在只依赖 Sub `PluginManager`；New API 管理使用独立 `NewAPIPluginHandler`，原路由和 step-up 保持。主站 HTTP 错误码与领域错误分开适配，不把 Gin/HTTP 状态移入引擎。
- 数据库无需搬表或重建版本；当前服务仍通过薄 facade 兼容旧调用。单点 `newapihost` 装配、具体 Provider/文件/凭据及 UI 全部独立尚未完成，后续能力缺口仍按兼容矩阵接续。

### 7.4 已实施：独立插件中心组件与原生页面分离

- `frontend/packages/newapi-ui` 提供不依赖主站 API、router、Store、i18n 或 `.s2plugin` 的 Vue 组件与公开类型。列表、筛选、详情、版本历史、上传、启停和恢复通过注入的 client/权限/翻译/Dialog 执行；默认只读，服务端权限和主站二次验证继续保留。
- `/admin/plugins` 使用该组件；原生列表移到独立 `/admin/native-plugins` 页面与菜单，继续使用原 `NativePluginsPanel` 和 native API。New API 列表不再请求 native 数据，不改变 native 安装/签名/运行行为。
- 源码比较、凭据清理、社区投稿/审核和外部市场目前仍为宿主插槽；未提供的入口不显示。本批完成 UI 核心包独立构建，不把全部子面板独立化或管理员浏览器验收标为完成。
- 包可以不加载主站测试 setup 单独测试、类型检查与 Vite 构建。主站 Tailwind 包含组件目录，保持当前配色和布局。构建产物不提交，包尚未单独发布。

### 7.5 已实施：生产装配与单次启停

- `backend/internal/newapihost` 提供生产 Wire 的单点装配，显式注入任务 Store/连接/结算通知 ports 创建 engine Executor；原 service ProviderSet 不再重复构造服务。
- `NewGenerationHostService` 只准备 facade，不自行启动 worker；`StartTaskExecutor` 一次性装配，重复启动、停止后启动均拒绝。兼容构造继续服务既有测试和调用者，不改变 Claim/事务范围。
- `Services.Stop` 幂等，先取消并排空 Task/主站维护，再关闭 JS pool；部分装配也能清理。旧 native 维护使用自己的认领方法，未接入第二条付费提交路径。
- 此目录当前是集成入口；具体账号、并发、上传、账务与协议 adapter 仍有一部分位于 service。没有为新系统创建独立钱包，没有自动迁移旧 native 视频执行，也尚未完成整站独立停用演练。

### 7.6 已实施：市场、社区审核、源码和清理 UI 继续提取

- `newapi-ui` 现另外包含 `PluginRegistryPanel`、`PluginMarketplacePanel`、`PluginSourcePanel`、`PluginCredentialCleanupPanel`，以及市场目录/摘要/撤回检查和有界源码差异工具。主站同名组件降为 HTTP、翻译、Dialog/Pagination 和 step-up 注入适配；原导入路径兼容保留。
- 面板收到纯接口，开发者、审核和市场页面不引用主站 API/Store/router。变更默认不可用，由宿主显式授予权限；服务端继续独立执行归属、revision、摘要和二次验证。
- 外部市场读端口可以替换，标准实现保留禁带凭据、大小/时间限制、安装前重查与固定 SHA。切换编辑对象后不接收旧本地文件异步读取的结果；离开页面取消列表、详情和网络读取。
- 独立包能够不加载主站 setup 运行面板和市场/源码工具测试。任务日志的组件继续提取见第 7.9 节，站点路由、账号和模型广场仍属于宿主；业务的具体协议/凭据/I/O adapter 与整站停用验收并未因此全部完成。

### 7.7 已实施：JS Task 结果生命周期与宿主适配规范

- `generation.ScriptDriver` 现拥有构造提交/查询描述符、解析受理/查询响应、终态状态、独立最终用量和产物处理。主站 Task session 仅注入固定上下文与有界 hook，签名/真实网络、文件准备和 Transform worker 排队继续留 adapter。
- 解析查询失败不写入半个终态；已确认受理的上游 ID 在立即完成 hook 失败时仍保留，避免丢失对账依据。资金仍只在原 Store 的同库事务中发生，没有因为提取 hook 就重复计费。
- 独立原版 Sora 回归把 engine Executor、JS hooks 和 fake ports 连起来，覆盖受理后重建脚本/执行器、终态、产物、实测结算意图、缺用量/未知状态审核和隐私。fake ports 不替代主站 PG/HTTP 资金联合验收。
- 宿主端口、事务/租约、凭据/文件/HTTP 责任、启停和验收规范见 [`newapi-engine/HOST_ADAPTER.md`](../../newapi-engine/HOST_ADAPTER.md)。通用原生路由、更多任务类型和完整 Provider/Transform/定价缺口仍保留。

### 7.8 已实施：旧原生视频维护独立生命周期

- 主站单独装配 `NativeVideoWorkers`，只接 native poll/settlement 小接口、账号、原网关和 Key；不注入 New API facade、JS pool 或 engine Store。原生查询和结算处理方法已迁入此 worker，不再由 `GenerationService.StartTaskExecutor` 启动。
- 两组 native worker 各保留 2 个并发、原认领/租约、GET 和原账本结算语义。共享 PostgreSQL 的原表和事实，不复制历史任务、不更改价格/资金、不发起生成。主站 cleanup 单独停止这两套 worker，数据库最后关闭。
- 真实阻塞认领上下文验证两种关闭顺序：关闭 New API 不取消 native 工作，关闭 native 不取消引擎工作；各自停止后自己的 I/O 均取消并排空。既有 native 状态/资金/HTTP 取消回归和隔离 PG 联合验收通过。
- 这解决后台维护被连带停掉的问题；不是整站可热卸载模块或生产升级/回退已验收的声明。原生管理员维护 facade 暂留主站，更多具体 adapter 收口和协议缺口继续接续。

### 7.9 已实施：独立任务日志组件

- `newapi-ui/TaskLogConsole` 提供列表、筛选、分页、详情、时间线和受鉴权下载；数据通过纯 `TaskLogClient` 注入，不直接依赖主站路由、HTTP、Store 或 i18n。主站 `TaskLogsView` 负责当前身份、页面布局、翻译和二次验证，保留原菜单与地址。
- 默认仅我的任务且不可修改。管理员展示与维护授权分别显式提供；资金对账工作台由宿主 `review` 插槽注入，未提供时隐藏此标签。旧原生维护为可选 host 扩展，不把 native 执行器迁入 UI 包。
- 切换角色/身份 client 或详情时取消旧请求和验证；晚到的读取/验证不覆盖新数据或执行旧操作。隐藏页面暂停只读刷新，返回后刷新，卸载清理全部计时器和请求。原生未知费用、真实零费用和视频生成/交付/账务状态继续分开展示。
- 独立与主站组件分别回归；本批保持原有页面样式，不把单元测试或生产构建当作真实管理员浏览器验收。

### 7.10 已实施：回复过滤独立 HTTP 适配

- `newapi-engine/replyfilter` 现独立拥有原版 UTF-16 遮盖/暂存、协议文本读取与重写、JSON/SSE 有界写入、停止与审计结果。它仅接收类型化 hook 回调，不反向 import Sub 或 Gin；原回归已随实现迁移。
- `New` 可直接接标准 `net/http.ResponseWriter`，内置延迟提交状态/大小记录与可选连接能力转发；状态无正文也会在 Finish 提交。现有 Gin writer 符合结构接口时直接复用，不重复维护状态。主站原导入路径仅保留类型与函数桥接，业务适配和资金位置不变。
- 宿主负责调用前的共享内存准入、隔离执行/超时、身份/权限、固定规则、真正上游取消、审计和结算；必须在 handler 返回前 Finish，不把 Gin context 放进后台。此提取不新增其他协议入口、WS 或独立的账本。

### 7.11 已实施：媒体检查与编解码核心独立

- `newapi-engine/mediahost` 拥有原 JSON 附件投影、回调、受限 fetch 流展开、文本/PDF/DOCX 提取、图片重编码和原子效果准备。原解析及真实隔离 worker 回归一起迁移，不 import Sub/Gin；依赖版本沿用原锁定值。
- 构造时由宿主注入临时内存准入函数，不把请求 Context 保存在 Source。引擎自己的两组进程限额与宿主既有请求预算同时生效，成功、失败和取消释放临时额度，不返还原 ingress 占用。独立使用也保留引擎限额。
- 主站原路径仅保留类型/构造桥接和共享预算适配；账号、文件归属、真实网络、审计、钱包和专用持久上传不迁入媒体核心。持久文件仍只读，不以提取代码为由扩大 multipart 或文件权限。

### 7.12 已实施：`newapi_engine.enabled` 开关、端口收口与第 8 节停用/回退演练

- 新增主站配置 `newapi_engine.enabled`（`NewAPIEngineConfig`，默认 `true`）。当设为 `false` 时，`newapihost.ProvideServices` 不启动 JS worker 池、不执行内置工厂注册、不启动凭据维护或 `generation.Executor` 后台认领，`ProvidePlugins` 与 `ProvideGenerations` 返回 `nil`（`host.Stop()` 幂等空操作）。
- 原引擎执行器连接、账号并发槽、官转五上传准备错误分类与结算后回调适配从 `service/generation_engine_host.go` 进一步收口至单点装配层 `newapihost/executor_adapter.go`（`NewTaskExecutorPorts`），`GenerationService` 仅暴露最小宿主能力端口。
- 主站网关、插件中心、开发投稿、请求处理、模型修饰符豁免与任务日志 handler 全面补齐 nil 安全守卫：`Plugins == nil` / `Generations == nil` 时，原生账号绑定/Chat/Responses/Images/Videos/Gemini 网关均零劫持透传并阻止新绑定（fail-closed），New API 专用管理与提交端点统一降级为 `503`；原生 `.s2plugin`（`PluginManager`）与 `NativeVideoWorkers` 不受影响。
- 已完成第 8 节全部离线与隔离 PG 17 演练验证：`TestNewAPIEngineDisabledConfigLeavesNativeSub2APIAndS2PluginOperational`、`TestNewAPIModuleWorksWithoutNativePluginManagerAndNeverMutatesNativeState`、`TestNewAPIModuleDisabledNativeGatewayPassesThroughAndPluginEndpointsDegrade503` 与 `TestGenerationDisableRollbackAndWorkerSwitchLocal`（验证停用→重启→切换新 worker 后历史任务 ID/版本/连接/价格快照与账本保留、幂等结算防重扣且仅单 worker 认领未结算任务）；`frontend/packages/newapi-ui` 独立构建（`build:newapi-ui`）与 Vitest（7 文件 32 用例）亦通过。生产环境真实升级/回退与浏览器实测仍在第 2 批用户验收范围。

## 8. 什么才算解耦完成

1. `newapi-engine` 使用 `GOWORK=off` 可独立构建/测试；不 import Sub 的 internal、native pluginapi、PluginManager、Gin handler 或具体数据库/账号 service。
2. 使用 mock 身份、连接、文件、出站和资金端口，可以独立跑通原版插件提交 → 查询 → 终态 → 产物/日志/结算意图；PG/实际资金测试另行通过。
3. 在主站关闭 New API 模块后，Sub 原有账号/原生插件/接口仍工作；关闭旧 native 插件后，已声明不依赖它的新模块路径仍工作。
4. 新模块的插件启停/版本/市场操作不会修改旧 Sub 插件的安装、配置或运行状态；两边错误不触发跨系统付费回退。
5. 在同库事务中证明冻结/结算/退款/已有 outbox 幂等；worker 切换时，同一任务不会重复认领执行或重复付费提交。
6. 历史任务 ID、版本、连接/价格/凭据快照和账本保留；可以禁用新入口并按演练方案回退，不覆盖已经产生的资金事实。
7. UI 包通过 client/权限/路由/i18n 注入，可单独构建；New API 插件文档和宿主适配文档分别能照做。

第 8 节 1～7 项现已在离线竞态与隔离 PostgreSQL 17 回归中完成演练验证；真实线上生产环境的停用/回退与管理员浏览器点击实测仍由用户在第 2 批发布验收时执行。

相关历史：[上一版完整 50 项清单](PLUGIN_SYSTEM_MIGRATION_PLAN.md)、[实际宿主能力矩阵](../../backend/internal/newapi/HOST_COMPATIBILITY.md)、[历史实施记录](../../backend/internal/newapi/IMPLEMENTATION_PLAN.md)。
