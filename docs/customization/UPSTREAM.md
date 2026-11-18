# 官方基线与二开维护

## 已固定的来源

| 项目 | 基线 |
| --- | --- |
| 官方 | https://github.com/Wei-Shaw/sub2api |
| 正式版本 | v0.2.23，2026-11-18 发布 |
| 官方提交 | `5af9afb588c4b014b3df9db7d4073af1f8ae01b6` |
| 官方 tree | `58ae0aff60632e214abdb4dec8e7a70e169b2f1e` |
| UI / 测智来源 | https://github.com/Callia218/clawai-sub2api |
| 来源提交 | `cf59fd96c4c73318d41c52098965eb549e37fbe1` |

保留真实官方 Git 提交作为共同祖先，`upstream` remote 指向官方。初次下载的 4,649 个官方文件已逐一验证 Git blob 哈希，重建 tree 与该正式版完全一致。`origin` 指向本项目仓库 https://github.com/daQzi/code1024，`main` 是 code1024 的发布主线（默认分支），`upstream` 继续指向官方。官方基线不再占用 `main`，而是以官方 tag 的形式保留在同一历史中，`main` 由官方基线 fast-forward 而来，未改写官方提交，便于后续同步正式版本。

## 定制边界

- 移植玻璃主题、响应式控制台外壳、首页、认证页面和公共组件，沿用官方站点配置；不预设来源作者的邮箱或品牌。
- v0.2.23 移除内置 `/pelican` 页面、三处菜单入口、前端 API/预览组件以及后端 `/api/v1/pelican/*` 接口和定时服务。升级后旧地址显示 404，旧定时任务不会再启动。
- 官方网关、认证、计费、路由和支付继续保留。质量巡检通过已有自定义菜单嵌入独立服务 `https://api.code1024.cc/quality/`，普通访客公开查看结果，管理操作由独立服务验证管理员身份。
- 历史 `backend/migrations/251_custom_pelican.sql` 保持文件名和内容不变，避免破坏迁移 checksum。`pelican_config` / `pelican_results` 历史表和数据保留，不执行删除迁移。
- 独立巡检使用自己的 SQLite、计划、配置与任务历史；主项目不承担其探针执行或定时管理。

## 构建与运行

使用 Go **1.27.4**（在官方 1.27 系列上升级安全补丁） 和 **pnpm 9**，本次安装使用 pnpm 9.15.9。不要用 pnpm 11 重新生成 lockfile。

```sh
# 仓库根目录
corepack prepare pnpm@9.15.9 --activate
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend build
pnpm --dir frontend test:run

cd backend
go test ./internal/server ./cmd/server
go test -tags unit ./internal/service -run 'Test(CustomBuild|UpdateService)'
go build -tags embed -o bin/server ./cmd/server
./bin/server -version
```

前端产物由 Vite 输出到 `backend/internal/web/dist`，embed 构建直接包含这些文件。

## 版本线与发布

- code1024 沿用与上游连续的正式版本号（首个发版 `0.2.22`，上游基线为 `0.2.21`），不使用 `-custom.N` 之类的后缀。版本号只包含数字段，因为更新器的版本比较会截断 `-` 之后的部分；同时正式号保证 GitHub `/releases/latest` 能正确解析。
- CI 构建通过 ldflags 写入 `main.BuildType=release`；手工 `go build` 默认仍是 `source`，不会误报可在线更新。
- 发布：推送 `vX.Y.Z` tag 触发 `.github/workflows/release.yml`，构建前端、5 个平台二进制与 `checksums.txt`，发布 GHCR 镜像（`ghcr.io/daqzi/code1024`），并创建 GitHub Release。Release 固定 `prerelease: false`，否则 GitHub 的 `/releases/latest` 不会返回该版本，在线更新会查不到。
- 在线更新：更新源固定为 `daQzi/code1024`（`backend/internal/service/update_service.go`），只下载本仓库构建的资产并校验 `checksums.txt`。仓库为公开仓库，更新器不需要 token。
- 在线更新只替换可执行文件，需要 systemd `Restart=always`（`deploy/sub2api.service`）或手工重启才能生效；Docker 部署应改为拉取新镜像并重建容器，不要在容器内核替换二进制。

Wire 变更在 `backend/cmd/server/wire.go` 维护并重新生成 `wire_gen.go`。

首次运行需要 PostgreSQL、Redis、官方安装向导/配置和管理员账户；不要连接生产库做试运行。数据库迁移由官方启动流程执行，正式升级先备份数据库。仅添加测智表不改变官方业务表；回滚应用版本可保留这两张表，删除会丢失测智配置和历史。code1024 v0.2.25 另合入三个官方迁移，分别新增分组 Ultra Fast 开关、更新分组日汇总状态及触发器函数、增加渠道 Ultra Fast 倍率。

自行构建 Docker 镜像：

```sh
docker build -t code1024:0.2.22 .
```

使用官方 compose 作为基础时，将应用 image 改为自己构建的 image，并按环境配置数据库、Redis、持久化目录和密钥。不要直接拉取官方镜像覆盖定制产物。本地验收使用原生 embed 二进制及独立 Docker Compose PostgreSQL 18 / Redis 8；应用与依赖均只绑定回环地址。运行配置、密钥和数据库保存在被 Git 忽略的 `.dev/local/`，不随源码提交。验收后已停止应用和这两个依赖容器，保留本地数据。未构建或部署应用 Docker 镜像。

## 持续同步官方正式版

这是一套可维护的同步流程，未创建自动合并、定时发布或自动部署任务。

1. 先 `git status --short`，保存并审查定制变更；从 `main` 创建升级分支，不要在脏工作区直接 merge。
2. 从官方 Releases 核实目标正式 tag，不以 main HEAD 代替正式版；记录其 commit。
3. 将官方 tag 获取到独立的本地引用，避免与 code1024 的同名发布 tag 冲突。在独立升级分支合并并保留上游提交历史。例如将占位值替换为已核实 tag：

```sh
git fetch --no-tags upstream 'refs/tags/<verified-release-tag>:refs/upstream/tags/<verified-release-tag>'
git switch -c upgrade/<verified-release-tag>
git merge refs/upstream/tags/<verified-release-tag>
```

本地已补全官方历史；若其他环境使用 shallow clone，历史不足以计算共同祖先时，先针对官方补足历史（必要时 `git fetch --unshallow upstream`），不要使用 `--allow-unrelated-histories` 或重新复制全仓覆盖。

4. 优先审查 `frontend/src/shell/` 菜单与官方页面/feature flags 的对应关系、共享组件 props/events、locale 完整性、router guards、后端 DI 接口、官方迁移及更新保护。
5. 冲突处理以官方安全、认证、计费、网关逻辑为准，再适配定制展示。新官方页面优先接入现有 shell，避免维护重复业务实现。
6. 执行前后端构建与相关测试；在隔离数据库验证迁移及登录/管理路径，使用受控 Key 验收一次测智。
7. 在本文件更新官方 tag / commit 和验证记录，保留来源及许可证说明。
8. 发布下一个 code1024 版本：提升 `backend/cmd/server/VERSION` 并打 `vX.Y.Z` tag 推送，由 Actions 产出 Release、二进制和镜像。

## v0.2.25 同步官方 v0.2.23

- 完整合入官方 v0.2.22 和 v0.2.23，包含 GPT-6.2 Sol / Astra、OpenAI 流式换号、批量扣费、调度快照和缓存写入计费修复。
- 分组 Ultra Fast 开关默认关闭，渠道倍率为空时使用模型默认定价。历史迁移 `251_custom_pelican.sql` 原样保留，官方脚本按完整文件名登记。
- 此版本同时包含 v0.2.24 的自定义菜单挂载功能，在线更新继续使用 code1024 发布源。
- 本地 Go 单元测试、前端 396 个文件 / 3,244 项测试、生产构建及嵌入式后端构建通过。独立 PostgreSQL 验证 v0.2.24 旧库升级后迁移记录由 303 条增至 306 条，并通过重跑校验、历史数据留存和 Chromium 实际登录检查。

## 初次二开验证记录

- 前端 `lint:check`、i18n 完整性、TypeScript 与 Vite 生产构建通过。
- 最终全套 Vitest 通过 394 个文件 / 3,212 项（2 workers）。一次高资源占用下的并行复验曾出现 2 项超时，单文件复测及降低并发后的全套复验均通过。
- 完整 `go test ./internal/server ./cmd/server` 通过；覆盖共享锁拒绝、定时开关重查、固定提示词/分组 Key 请求、结果入库错误、查询失败、关闭行为、公开响应脱敏和管理接口权限拦截。管理路由测试验证中间件挂载和拒绝行为；独立本地数据库中已验证管理员登录、JWT 用户查询、测智状态与画廊读取，以及官方管理员承诺未确认时管理接口返回 423。
- Chromium 实测公共首页 1440px/390px 无横向溢出、登录表单可见、深色切换有效；管理员测智空态和配置面板可打开，零模型请求。该界面验收使用隔离响应 fixture，并非真实认证/数据库端到端验收。
- Chromium 在父页 nonce CSP 下实测：模型脚本与尺寸桥接执行，父页 DOM 访问失败，外部 fetch 被阻止。
- Wire 重新生成成功，应用依赖版本及锁文件未修改。
- `go test -tags unit ./internal/service -run 'Test(CustomBuild|UpdateService|CheckCode1024)'` 通过，更新器只认 `daQzi/code1024` 的 Release。
- `go test -tags unit ./internal/server ./cmd/server ./internal/web` 通过。
- 完整 Go embed 构建通过，实际执行 `bin/server -version` 返回 `0.2.22`。
- 使用空临时数据目录在 `127.0.0.1:5182` 启动实际 embed 二进制，`/setup/status` 返回 200 / needs_setup=true，嵌入前端在浏览器可加载。未提交安装向导，无数据库写入；验收进程已停止。
- 2026-11-15 使用独立 PostgreSQL 18 / Redis 8 完成全新初始化及官方迁移流程，确认 `pelican_results` / `pelican_config` 存在，`/health` 返回 200、`/setup/status` 为已完成，实际首页与浏览器管理员登录无页面异常。官方部署运营承诺保留为使用者本人确认；未确认前后台管理/测智配置接口按官方规则阻止访问。
- 测智没有配置渠道或启用定时，未执行收费模型调用；配置保存/真实生成尚待使用者确认承诺并提供授权渠道后验收。未构建应用 Docker 镜像，未部署到服务器。

## 2026-11-16 发布验收

- 仓库已转为公开，`main` 为默认分支与发布主线；官方 v0.2.21 仍在同一历史中作为 tag 可追溯。
- `v0.2.22` 已发布：Release 为 Latest、`prerelease=false`、`draft=false`，资产含 5 个平台包与 `checksums.txt`，下载校验值与 `checksums.txt` 一致。
- GHCR `ghcr.io/daqzi/code1024` 的 `latest` / `0.2.22` / `0.2` / `0` 均可匿名拉取 manifest（200）。
- 无 token 实测更新链路：以 `0.2.22` 查询返回 `hasUpdate=false` 且无 warning；以 `0.2.21` 查询返回 `hasUpdate=true`、`latest=0.2.22`，资产里包含 `checksums.txt` 与当前平台包。
- `raw.githubusercontent.com/daQzi/code1024/main/deploy/install.sh` 可匿名访问且 `GITHUB_REPO="daQzi/code1024"`。
- 未执行会覆盖二进制的真实就地升级；该路径仍需在隔离主机上验收。

## v0.2.23 交付边界

移除内置鹈鹕测智及其后台执行链路，不迁移或删除 PostgreSQL 历史数据。旧版本验收记录保留在本文，描述的是当时状态。
新质量巡检由独立服务发布与回退；code1024 使用既有管理员自定义页和用户自定义页承载它。
升级按发布镜像标签执行，保留 0.2.22 镜像与 Compose 备份；回退程序无需回滚数据库。

本地验证：前端 ESLint、类型检查、34 个测试文件 / 639 项关键测试及生产构建通过；
Go 1.27.3 的 `go test ./internal/server ./cmd/server` 通过，Wire 重新生成仅移除鹈鹕依赖。
历史迁移、go.sum 和 pnpm-lock.yaml 均未改变；go.mod 的 Go 最低版本升级至 1.27.4。
独立巡检另通过 315 项 Python、43 项 Node 测试以及本地 HTTPS 模拟上游/浏览器验收；
生产已同步元数据，未发起真实模型请求。

发版安全检查发现现有 Go 1.27.3 的 `net/http` 存在 GO-2026-6859（HTTP/1 MaxBytesReader header race 导致服务崩溃）。
本版同步升级 go.mod、Docker 构建默认镜像、CI、安全扫描与 Release 版本断言至 Go 1.27.4，不改变业务依赖版本。
