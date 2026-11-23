# New API 插件中心 UI

独立 Vue 3 组件包，当前为仓库内 private 包。插件列表、详情、版本、启停、恢复、源码比较、社区投稿/审核/离线试运行、外部市场、凭据清理和任务日志组件由本包实现。无主站 API、路由、Store 或原生 `.s2plugin` 运行时依赖。

```vue
<PluginCenter
  :client="client"
  :t="translate"
  :locale="locale"
  :dialog-component="Dialog"
  :editable="canManagePlugins"
  :run-mutation="authorizeMutation"
/>
```

- `client` 实现 `src/types.ts` 的 `PluginCenterClient`。HTTP、身份凭据和错误转换由宿主负责；列表和版本查询收到 `AbortSignal`。可选 `remove`、`pricing`、`dryRun` 分别提供受保护的自定义版本删除、实际账号定价、固定版本离线沙盒；价格和沙盒读取同样接收取消信号。服务端必须保护内置版与被引用版，沙盒不得读取账号或执行网络请求。
- `editable` 默认 `false`。`runMutation` 必须执行宿主授权/二次验证后再调用传入的操作；服务端仍需独立检查权限。
- `t` 为翻译函数，`locale` 为当前语言。翻译键沿用 `admin.plugins.*` 和 `common.*`。
- `dialogComponent` 接收 `show`、`title`、`width`，详情会额外传入 `placement="right"`；触发 `close` 并渲染默认插槽。宿主可支持右侧抽屉或保留自己的弹窗样式。
- `iconComponent` 可提供宿主已有的模型图标（接收 `model`、`size`）；常见任务插件品牌图标在本包内展示。
- `selectComponent` 可为插件中心、市场、投稿、源码对比和任务日志提供宿主下拉框。接收 `modelValue`、`options`（`{ value, label }`）、`disabled`、`ariaLabel`，触发 `update:modelValue`。省略时使用原生下拉框。
- 本包沿用宿主 Tailwind 主题和 `btn`、`input`、`card` 类；宿主 Tailwind 的 content 必须包含本包 `src/**/*.{vue,ts}`，并提供现有 `dark`/`primary`/`dark-*` 主题变量。

可选插槽：`header-actions`、`cleanup`、`registry({ mode })`、`community-market({ reload })`、`external-market({ plugins, reload })`、`detail-actions({ plugin })`、`pricing-actions({ plugin })`、`source({ plugin, versions, mode })`。`mode` 区分 `source` 与 `diff` 标签，可传给 `PluginSourcePanel` 自动展开对应内容。未注入的管理/市场入口不显示；宿主可组合下面的独立面板，也可提供自己的面板。

## 独立面板与端口

| 组件 | 需要宿主注入 |
| --- | --- |
| `PluginRegistryPanel` | `PluginRegistryClient`、`mode`、翻译、Dialog、Pagination、二次验证；`canWrite` 和 `canInstall` 默认 false |
| `PluginMarketplacePanel` | `PluginMarketplaceClient`、`MarketplaceReadClient`、`PluginSourceClient`、已安装列表、语言与 UI/验证；`editable` 默认 false |
| `PluginSourcePanel` | `PluginSourceClient`、版本身份/列表及翻译；读取后重新验证身份与原始 UTF-8 SHA256 |
| `PluginCredentialCleanupPanel` | `PluginCleanupClient`、语言、翻译与二次验证；`editable` 默认 false，失败只显示公开提示 |
| `TaskLogConsole` | 已按身份限定的 `TaskLogClient`、语言、翻译、Dialog/Pagination；管理员展示和维护授权分别显式提供 |

所有 client 都是纯 TypeScript 接口，定义见 `src/types.ts`。主站同名 Vue 文件仅负责注入 HTTP、i18n、公共组件和 step-up 对话框；开发者页面与管理员页面可共用组件，服务端须继续按当前身份限制客户端请求。`RunPluginMutation` 保留操作结果类型，`isCancelled` 用于忽略宿主主动取消验证。

`MarketplaceReadClient` 允许宿主替换市场读取。包导出的标准浏览器实现使用不携带凭据/Referer 的有界 fetch，安装前重新读取索引、撤回列表和固定摘要。不要把本站 Key 注入公开市场读取。源码差异算法和市场协议工具也在包内，原主站工具路径仅兼容 re-export。账号编辑、模型广场和站点路由仍由主站拥有，本包不包含完整站点。

## 任务日志接入

`TaskLogConsole` 的 `client` 提供 `listTaskLogs`、`getTaskLog` 及可选 `downloadTaskContent`，接口不接客户端可选择的管理员身份。宿主应把当前认证身份固定在 client 中；切换角色/账号时替换 client，组件会取消旧请求、关闭详情并清除旧数据。`admin` 只控制展示，不授予服务器权限。

- 默认只读、仅“我的任务”。`admin` 开启全部/我的视图；有 `review` 插槽时才展示对账入口。本站对账工作台仍经宿主插槽提供，钱包事务不进入 UI 包。
- 旧原生历史可作为查询投影展示；原生维护是可选 host 扩展。必须同时传 `editable`、`runMutation` 和 `client.maintainNativeTask` 才显示操作。`cancelMutation` 用于关闭宿主二次验证；切换详情/身份或卸载后，即使验证晚到也不能再执行旧操作。
- 列表/详情每 5 秒只读刷新；隐藏页面取消刷新读取并暂停，返回后重新读取。显式下载/维护不因切换浏览器标签被自动取消；关闭详情和卸载仍取消它们。未知费用保持未知，日志查询不发起生成或扣款。
- HTTP 和鉴权下载由宿主执行。读取错误显示公开翻译提示，不直接展示传输层可能携带的 URL/凭据。

在 `frontend/` 使用现有依赖运行：

```sh
pnpm run build:newapi-ui
pnpm exec vitest run --config packages/newapi-ui/vitest.config.ts
```

构建输出 `dist/newapi-ui.js`，Vue 为外部 peer dependency。独立测试不加载主站 setup；架构检查禁止本包导入主站路径。生产主站直接引用包源码，路由 `/admin/plugins` 与 `/admin/native-plugins` 分别提供两套系统的管理入口。

`ProviderToolPricePanel` 通过 `ProviderToolPriceClient` 读写 USD/千次的工具售价表。
默认只读；保存同时要求 `editable` 和 `runMutation`，宿主负责权限、revision CAS 与二次验证。
更换 client/权限或卸载时取消旧请求与授权，晚到的授权不会执行旧 client 的保存。
冲突保留草稿，传输错误只显示公开译文；组件不持有钱包或自行计算扣款。
