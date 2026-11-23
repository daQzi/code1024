# 视频 Task 插件登录与凭据

参考 New API 固定基线 `48f9df96ea7c68eae04a0f3687869f232f48929f` 的
`relay/channel/task/jsplugin/credential.go`。任务插件的 `login/refresh/revoke/scheduled`
复用本站已实现的账号登录卡片、密文存储、PKCE、会话归属、持久租约和有界维护队列。
这适用于当前已接入的 OpenAI Videos 与 xAI/Ark 原生视频入口，不代表所有 Task 路由已完成。

## 管理员使用

在账号管理中添加普通 API Key 账号、选择视频 Task 插件并保存。插件有 `login` 时可以
不填占位 Key，再从保存后的凭据卡片登录、续期或撤销。Task-only 插件不需要伪造 Provider
relay 路由。拥有 `scheduled` 的插件仍需管理员明确选择是否允许自动暂停账号。

- 登录和续期数据只进账号的密文凭据表；不把 credential JSON 填进普通 API Key 字段。
- Task hook 得到自己的 `credential` 数据、`auth.authHeader` / `authHeader`；此时
  `apiKey` 为空，不把整个密文状态或别的账号数据交给脚本。
- 实际上游提交、查询和受保护下载先应用授权 headers/query，再执行 `signRequest`。
  `credentialless` 媒体不应用凭据、不签名，仍走原有公开地址和重定向校验。
- 管理登录不直接发起生成；后台续期不占住 JS worker 等待另一个 worker。仍共用账号
  并发、进程和内存上限。被撤销、要求重登、到期且不能续期或显式暂停的连接不应进入新任务。

## 旧任务继续使用哪个账号

创建时在解码之后、估价和冻结之前固定 Task 的授权身份。带 `accountId` 的 grant
按插件和该上游身份绑定；匿名 grant 使用宿主随机身份，续期保持，重新登录更换。
快照只保存非机密身份摘要与原连接摘要，不保存 access/refresh token。

生成后台 worker 重启后，从账号密文存储取得同一身份的新 token。更换连接、插件版本、
配置、登录身份或撤销后，旧任务不能悄悄使用替代凭据。升级前没有管理身份的任务仍是
静态 Key 任务，不自动领用新 grant。

冻结事务再次锁住凭据公开投影，并检查当前连接、身份和撤销状态；并发更换登录会整笔
回滚任务插入和冻结。父任务引用还必须与派生任务属于同一登录。公开响应与任务日志
不返回私有绑定。

续期在请求前完成。已发送的付费提交不因 401 或换 token 自动重发；沿用原提交不确定性、
后台查询和人工对账流程。重新查询或下载也不会重新生成。

## Google Service Account / oauth2_jwt

视频 Task 的 `meta.auth.type = "oauth2_jwt"` 与固定基线一样由宿主换取授权；管理员在普通
API Key 输入中保存 Service Account JSON。宿主以 RS256 签署固定 Google cloud-platform
scope 的 assertion，经账号代理访问固定 `https://oauth2.googleapis.com/token`，忽略自带的
`token_uri`。实际 HTTP 保留公网目标、禁重定向、15 秒时限、32 KiB 响应上限与独立共享内存准入。

Task 仅收到 `authHeader`、`auth.authHeader` 与 `auth.projectId`，`apiKey` 为空，私钥不进入普通
Task / 签名上下文。令牌按账号、版本/源码、连接/配置、Key、代理隔离，复用现有有界合并缓存；
遵守真实有效期和最多 25 分钟缓存。一名调用者取消不取消其他调用者共享的授权交换。
JWT 任务额外固定 Key 的连接摘要，更换 Service Account 或连接后旧任务停止使用替代身份。
经另一个 New API 网关调用时直接用网关 Key 的 Bearer Header，不执行厂商 JWT；持久管理 grant
也可替代厂商 JWT。真实厂商测试仍待 R8，离线夹具不代表 Google 已实际授权。

## 范围

当前视频 Task 支持插件持久登录 grant、静态 Key / exchangeKey 和 Google oauth2_jwt。
通用 native routes、其他任务类型和完整媒体宿主继续以 `HOST_COMPATIBILITY.md` /
`COMPATIBILITY_BACKLOG.md` 为准；不能把本增量叫作所有 New API 渠道兼容。
真实厂商登录、回调、刷新与素材下载仍需 R8 联调。
