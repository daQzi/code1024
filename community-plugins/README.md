# Sub2API 独立社区渠道插件

本目录存放按 [`newapi-engine/PLUGIN_DEVELOPMENT.md`](../newapi-engine/PLUGIN_DEVELOPMENT.md) 拆分为可独立打包、审核和上架社区市场的渠道视频插件包（遵守 `AGPL-3.0-or-later`）。

| 目录 | 独立社区 Key | 版本 | `plugin.js` SHA256 | 用途 |
| --- | --- | --- | --- | --- |
| [`sub2api-dreamina/`](sub2api-dreamina/README.md) | `community-sub2api-dreamina` | `1.0.0` | `5086be00e4359bab23bbabdeeeabeedba52fa0bf114149f1501dd9a85b6981e4` | 官转二 · Sub2API 视频渠道 |
| [`sub2api-official5/`](sub2api-official5/README.md) | `community-sub2api-official5` | `1.1.0` | `7dba6a094fc9e2807370005a8c27f4621f818e60b155338d8fb46a0728886cb9` | 官转五 · Sub2API 视频渠道 |

## 与内置历史版本的关系（重要）

- 项目原 `newapi-engine/builtin/sub2api-dreamina` 与 `newapi-engine/builtin/sub2api-official5` 源码、版本号及 SHA256 **保持原样不动**，确保已绑定的旧账号、在途或历史任务、固定版本快照及账务对账不受任何影响。
- 本目录提供的是完全自包含的独立社区发行包（移除跨渠道分支并采用 `community-*` key）：
  - 管理员可直接通过 `/admin/plugins` 安装（与内置版本并存，不覆盖历史固定版本）。
  - 向“插件开发”社区市场投稿时，按页面分配的开发者命名空间修改 `meta.key` 并递增 `meta.version` 后审核发布。
  - 若需要官转五 1.1.0 宿主分片上传准备工作流（由 `backend/internal/service/generation_preparation.go` 按内置精确 SHA256 绑定），请继续在账号中绑定内置 `sub2api-official5@1.1.0`；独立社区包 `community-sub2api-official5` 面向文生及预先上传的 `storage_object_id` 引用。

## 离线校验命令

在仓库根目录执行（需确保 `GOWORK=off` 在独立 `newapi-engine` 模块下运行）：

```sh
cd newapi-engine
GOWORK=off go run ./cmd/pluginctl lint ../community-plugins/sub2api-dreamina/plugin.js
GOWORK=off go run ./cmd/pluginctl test ../community-plugins/sub2api-dreamina/plugin.js --fixture ../community-plugins/sub2api-dreamina/fixture.json

GOWORK=off go run ./cmd/pluginctl lint ../community-plugins/sub2api-official5/plugin.js
GOWORK=off go run ./cmd/pluginctl test ../community-plugins/sub2api-official5/plugin.js --fixture ../community-plugins/sub2api-official5/fixture.json
```

通过离线回放仅验证确定性 JS hooks；接入真实计费账号前仍需先用有限额度的测试账号按第 2 批真实验收清单联调上游参数、素材与最终账单。
