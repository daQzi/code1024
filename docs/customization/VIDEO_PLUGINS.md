# 视频协议插件：接入与开发规范

更新日期：2026-11-22。适用于本站已经接入的 New API API v1 Task 视频契约。

插件是“上游翻译员”：账号保存地址、Key、模型映射和分组；插件转换请求、状态和下载；本站负责排队、任务归属、日志、冻结、结算与退款。同协议的不同站点复用一个插件，分别创建账号。不同协议开发不同 `plugin.js`，下游统一使用 `/v1/videos`。Sub 原生 `.s2plugin` 保留独立运行时。

## 1. 管理员接入

1. 在「插件管理」的系统插件目录启用 `OpenAI Compatible Video`，key 为 `sub2api-openai-video`，当前版本 `1.0.1`。新增系统版本自动登记，默认停用，不自动替换已有账号版本。绑定 `1.0.0` 的账号需显式选择新版本；已经接受的任务继续使用原版本。
2. 在账号管理新建 OpenAI 平台普通 API Key 账号，填写上游地址和 Key，选择插件固定版本；上游类型按实际情况选择“原厂”或“New API”。
3. 设置分组、并发，以及具体的公开模型到上游模型映射。同名模型也要显式填写：

   ```json
   {"my-video":"vendor-movie-v2","another-video":"another-video"}
   ```

4. 设置视频售价或分组视频价格，再用该分组的本站 Key 验证创建、查询、下载和最终账单。

示例模型名必须替换为目标 Key 已获授权的型号。插件 `models:["*"]` 只声明驱动能处理任意具体模型名；本站只开放账号映射中的精确模型。`{"*":"vendor-video"}`、`video-*` 或映射目标 `*` 不能代替具体模型。模型目录和调度使用相同限制，不能把所有文本模型当成视频模型；账号仍受分组和调度状态限制。

### 地址与提交格式

| 上游地址 | 实际提交路径 |
| --- | --- |
| `https://upstream.example` | `/v1/videos` |
| `https://upstream.example/v1` | `/v1/videos` |
| `https://upstream.example/proxy` | `/proxy/v1/videos` |
| `https://upstream.example/proxy/v1/` | `/proxy/v1/videos` |

默认向上游发送 multipart。仅当上游明确支持 JSON 时，将插件配置 `submission_format` 改为 `json`。带文件时始终使用 multipart，boundary 由宿主生成。

本插件要求上游提供 `/v1/videos` 创建、`/v1/videos/{id}` 查询和鉴权的 `/v1/videos/{id}/content` 下载。只返回外部成片 URL、使用其他路径或状态字段的上游需要对应协议插件。不能只根据“兼容 OpenAI”判断视频协议相同。

## 2. 下游调用

使用本站 Key、公开模型名及本站返回的 `video_...` 编号：

```bash
curl https://YOUR_SITE/v1/videos \
  -H 'Authorization: Bearer YOUR_SITE_KEY' \
  -H 'Idempotency-Key: video-order-001' \
  -H 'Content-Type: application/json' \
  -d '{"model":"my-video","prompt":"海边日落，镜头缓慢推进","seconds":"8","size":"1280x720"}'

curl https://YOUR_SITE/v1/videos -H 'Authorization: Bearer YOUR_SITE_KEY'
curl https://YOUR_SITE/v1/videos/VIDEO_ID -H 'Authorization: Bearer YOUR_SITE_KEY'
curl https://YOUR_SITE/v1/videos/VIDEO_ID/content \
  -H 'Authorization: Bearer YOUR_SITE_KEY' -o result.mp4
```

同一业务订单重试时保持 `Idempotency-Key` 和原请求一致。已接受任务先重放原记录；同键换正文会冲突。网络结果不明的提交进入待核对，不盲目创建第二个付费任务。

单图参考使用标准 multipart：

```bash
curl https://YOUR_SITE/v1/videos \
  -H 'Authorization: Bearer YOUR_SITE_KEY' \
  -H 'Idempotency-Key: video-order-002' \
  -F 'model=my-video' -F 'prompt=让镜头缓慢推进' -F 'seconds=8' \
  -F 'input_reference=@reference.png'
```

该插件只接受一个 `input_reference` 文件，PNG/JPEG/WebP 最多 32 MiB；其他文件字段、多文件或同名文本与文件混用会被拒绝。多素材协议应另写插件，见 [FILE_HOST.md](../../backend/internal/newapi/FILE_HOST.md)。JSON 的 `metadata` 为对象，multipart 的 `metadata` 为 JSON 对象字符串；其具体字段是否可用由上游决定。

内容接口支持 GET/HEAD 和受支持的 Range 请求。下游 Authorization 不替代上游 Key。任务固定创建时的连接、版本和配置，修改账号默认地址或默认版本不会把旧任务送到新上游。

## 3. 定价与加价

账号 `newapi_video_pricing` 按公开模型匹配，支持 `*` 价格兜底；没有账号价格时使用既有分组视频价格。价格 `*` 不扩大模型授权。所有样例金额仅用于解释公式。

按秒固定售价：

```json
{"my-video":{"mode":"fixed","unit":"second","unit_price":"0.02"}}
```

知道上游单价时，使用已有成本加价规则：

```json
{
  "my-video": {
    "mode":"cost_plus",
    "source_currency":"USD",
    "source_unit":"second",
    "source_unit_price":"0.01",
    "exchange_rate":"1",
    "markup":"1.5",
    "service_fee":"0",
    "minimum":"0"
  }
}
```

`markup=1.5` 表示成本乘 1.5。本站以 USD 记账，`exchange_rate` 为来源币种到 USD 的换算系数；还应用任务创建时固定的分组倍率。

通用视频协议没有本插件可依赖的统一成本字段，因此不自动发现价格、不把任意 `cost` 当正式账单。上述规则使用管理员填写的上游单价和最终实际秒数。按正式上游账单加价，需要协议插件明确提取最终成本和币种，见 [CHANNEL_ADAPTERS.md](../../backend/internal/newapi/CHANNEL_ADAPTERS.md)。

- 按秒收费要求请求提供正数 `seconds`，也接受本站 `duration` 别名；同时提供时必须相等。不猜默认时长，上限 3600 秒，实际合法档位由上游决定。
- 创建时冻结估计费用，完成后以查询返回的真实 `seconds` 结算。例如 8 秒 × 0.02 冻结 0.16，最终 6 秒结算 0.12。
- 缺少最终秒数、返回 `null` 或非法值时，仍保存任务成功及可下载状态，费用待对账，不把估计当实价；超过冻结上限同样待处理，不自动多扣。
- 按次售价使用 `unit=request`，不要求秒数。确认失败的任务按现有规则退款。
- 缺价不默认免费，免费须明确零价；插件不直接操作余额或写账本。

`1.0.1` 修复了非法最终秒数阻止任务进入终态的问题。确认失败时不读取无效用量，按既有流程退款；成功但用量缺失时保留冻结等待核对，不重复提交生成。旧版本在途任务仍按原逻辑处理，升级账号不会改写它们；如已卡住，需确认上游状态后走管理员对账。

## 4. 开发协议插件

完整可复制模板：[sub2api-openai-video/plugin.js](../../newapi-engine/builtin/sub2api-openai-video/plugin.js)。该文件使用实际运行的 JS 契约，有 worker、模拟 HTTP、私有文件和资金服务回归。

复制后修改唯一 key、作者、描述和版本；同版本源码不可覆盖。固定协议优先声明具体模型，通用驱动才用 `*`。实际宿主边界见 [HOST_COMPATIBILITY.md](../../backend/internal/newapi/HOST_COMPATIBILITY.md)，编译通过不等于完整兼容 New API。

| 元信息 | 约定 |
| --- | --- |
| `meta.apiVersion` | 当前为 `1` |
| `meta.key/name/version` | 插件身份、显示名、不可变版本 |
| `meta.models` | 具体模型或家族；最终通过账号映射为具体公开模型 |
| `meta.protocols` | 对下游提供视频时包含 `openai_video` |
| `meta.fetchMode` | 普通逐任务查询使用 `per_task` |
| `meta.upstreams` | 按实际支持声明 `vendor` / `new_api` |
| `meta.channelConfig` | 宿主支持的字段 schema，用于表单、默认值及校验；不是完整 JSON Schema |
| `meta.usageSchema` | 用量字段、类型和单位，如 `seconds` 的 `unit:second` |

| Task 钩子 | 职责 |
| --- | --- |
| `protocols.openai_video.decodeRequest(ctx)` | `ctx.body` 为 JSON/multipart 信封；校验输入，返回 `{kind:"submit",model,action,requestBody}` |
| `extractUsage(ctx)` | 从规范化请求提取估计用量，如 `{seconds:8}`，未知不猜 |
| `buildSubmitRequest(ctx)` | 使用 `upstreamModel/baseUrl/apiKey/config/requestBody/files` 返回 HTTP 描述符 |
| `parseSubmitResponse(ctx,response)` | 校验上游接受证据，返回 `{taskId,taskData}`；response 含 `statusCode/headers/body` |
| `buildQueryRequest(ctx)` | 使用私有 `ctx.taskId` 构造查询 |
| `parseTaskResult(ctx,body)` | 映射 `QUEUED/IN_PROGRESS/SUCCESS/FAILURE/UNKNOWN` 及进度，未知不能猜成功 |
| `extractUsageOnComplete(task,result,body)` | 提取完成时真实用量；宿主单独保存最终证据 |
| `listArtifacts(task)` | 成功成片返回如 `[{key:"video",type:"video"}]`，失败不造制品 |
| `buildContentRequest(ctx)` | 使用 `upstreamTaskId/artifactKey` 和客户端 GET/HEAD 方法构造下载 |
| `protocols.openai_video.render(ctx,task)` | 使用本站 ID、公开模型；结果仍受宿主公开字段和脱敏规则约束 |

解码发生在选账号之前，没有账号配置、Key 或联网能力，不能依赖 `ctx.config`。需要不同解码规则时，使用明确请求字段或不同协议插件。不要用 JS 全局变量存任务进度：任务可能由另一个 worker 或重启后的进程继续处理。

JSON 描述符为 `{url,method,headers,body}`。multipart 使用 `bodyType:"multipart"` 和 `parts`，以 `{name,fileRef,filename}` 引用 `ctx.files`。不要把二进制、服务器路径或存储密钥放入 JS。宿主执行 HTTP，并检查目标、凭据、大小、时限和资源限额。

插件不建立第二套余额、退款、轮询线程或公共任务表。Key、原始响应、签名 URL、私有文件引用不能进入公开 render、任务日志或模型广场。特殊签名、OAuth、文件能力和高级操作按现有兼容文档逐项确认，未支持能力必须明确拒绝。

## 5. 验证与发布

1. 修改模板后，在「插件开发」以模拟输入试运行。试运行不联网、不操作账本，也不代替 HTTP 和资金验证。
2. 管理员上传 `plugin.js`；社区作者走命名空间投稿、审核、发布流程。安装后查看兼容报告，再启用。
3. 使用测试账号验证有效/无效请求、模型映射、文生/图生、状态转换、真实用量、下载鉴权和跨用户访问。付费联调先明确模型权限和额度。
4. 模拟超时、2xx 无任务号、未知状态、缺账单、下载失败和重复请求，确认不重复提交或扣费、不提前记成功。
5. 升级先加版本并测试，再显式修改账号固定版本；在途任务保留旧版本和配置。

本仓库定向回归：

```bash
cd newapi-engine
GOWORK=off go test -race ./...
GOWORK=off go run ./cmd/pluginctl lint builtin/sub2api-openai-video/plugin.js
cd ../backend
go test -tags=unit ./internal/service -run '^(TestGenericVideoPlugin|TestGeneration|TestPluginFactoryCatalogAndHostCompatibility)' -count=1
```

完整本地验收可从仓库根目录运行：

```bash
# 需要本机已有 Go 和 PostgreSQL；脚本不安装依赖。
./backend/scripts/test-video-plugins.sh

# PostgreSQL 未加入 PATH 时，例如 macOS Homebrew：
PG_BINDIR=/opt/homebrew/opt/postgresql@17/bin ./backend/scripts/test-video-plugins.sh
# Go 未加入 PATH 时，可同时指定 GO_BIN=/absolute/path/to/go。
```

脚本忽略外部传入的数据库 DSN，自建临时 PostgreSQL，数据库名以 `_test` 结尾，仅开放私有 Unix socket；退出时停止并删除自己的集群。覆盖 JS 运行时、视频服务、数据库和 HTTP handler 的 `-race` 定向测试，包括重启恢复、原连接/价格固定、同键重试不重复提交或扣费、失败退款、缺用量及提交不确定时待核对、日志归属/脱敏和 GET/HEAD/Range 下载。认证上下文、账号目录与并发缓存是测试夹具，上游是本地 HTTP 模拟服务，不能替代完整鉴权、Redis、管理页面或真实厂商联调。

当前已提取独立运行时/契约模块并提供可运行的校验/离线回放 CLI，见 [newapi-engine](../../newapi-engine/README.md)。持久任务引擎、控制层、独立 UI、完整 SDK 及全部协议兼容仍是后续工作。通用插件使用模拟上游验证，未因此验证真实付费渠道。

### 通用任务查询与产物

通过本站插件生成的视频任务，也可用 New API 的通用读取路径查询。使用创建时同一用户、API Key 和分组：

| 方法 | 路径 | 返回 |
| --- | --- | --- |
| GET | `/v1/tasks/:key` | `task_id`、`platform`、`status`、百分比 `progress`、`fail_reason`、创建与完成时间 |
| GET | `/v1/tasks/:key/artifacts` | 任务 ID 与已声明产物的 `key`、`type`、可选 `mime_type`、本站 `content_url` |
| GET / HEAD | `/v1/tasks/:key/artifacts/:artifact_key/content` | 下载或读取已声明产物的响应头 |

`:key` 是 `/v1/videos` 返回的本站任务 ID。进行中的任务返回空产物列表，提前下载返回 409；其他用户、Key 或分组的任务返回 404。列产物仅检查已保存的结果，不联系上游；下载复用原鉴权代理，支持上游提供的 Range 能力。响应不暴露上游任务 ID、私有 URL 或认证信息。同一任务也可用下面的按插件入口创建。

本站还支持 `POST /v1/tasks/:key/cancel`，用于取消尚未被 worker 认领的排队任务。重复调用返回同一取消结果；若已在提交、已被上游接收或提交状态不明，返回 409，需按原流程查询或对账。取消后任务仍可查，通用状态为 `FAILURE`，原因为提交前取消；取消接口单独返回 `status: "canceled"` 及 `billing_status`。已冻结费用由原结算 worker 归还，`pending` 代表退款尚未确认。查询、下载和取消不要求 Key 还有可用额度，但仍须通过原鉴权与任务归属检查。

`POST /v1/tasks/:key` 创建已接入的视频 Task。这里的 `:key` 是插件名（例如 `sub2api-openai-video`），不是任务 ID。请求使用与 `/v1/videos` 相同的非流式 JSON，`model` 填分组内的公开模型名。系统只选当前分组中绑定同名插件、支持该模型的账号，以该账号固定的版本和映射处理请求。成功返回 `id`、`task_id`、`status`、`model`、`created_at`，`Location` 指向 `/v1/tasks/<任务 ID>`。

建议提供 `Idempotency-Key`。同一 Key、分组、插件和原始请求会复用已受理任务；改插件、换入口或改正文会冲突。此入口暂只支持普通上游平台分组和现有视频驱动，不接客户端 multipart、音乐、图片或 Responses Task；直接传文件继续使用 `/v1/videos`，预上传引用仍可在 JSON 中提供。
