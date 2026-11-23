# Task 私有输入素材

本阶段将 New API 固定基线 `48f9df96ea7c68eae04a0f3687869f232f48929f` 的 `ctx.files`、multipart `parts[].fileRef` 和 JSON `__fileRef` 接入持久 Task 宿主。插件只收到字段、文件名、MIME、字节数和 opaque ref，不收到磁盘路径、数据库位置或可公开访问的素材 URL。

## 已开放入口

`/v1/videos`（及根路径 `/videos`）、通用按插件提交 `/v1/tasks/:key`（排除无文件合同的内置 `sunoapi`，其发送 multipart 返回 `415` fail-closed）以及已声明 `meta.routes` 的原生提交入口 `/v1/tasks/:key/native/*subpath` 与根路径别名均已接入相同的有界直接 multipart 暂存宿主：PNG/JPEG/WebP `input_reference` 最多 32 MiB；本站多素材扩展允许最多 4 个文件、每个最多 32 MiB、整个请求最多 129 MiB；其他媒体字段须为宿主能检测到的 image/video/audio 类型。字段 `model`、`prompt`、`seconds`、`size` 等按标准文本发送；文本字段仍要求唯一，文件字段可重复，`metadata` 保留 JSON 对象语义。实际字段、数量和媒体是否可用于某个模型，由插件 decoder 校验；原版 Sora 仍只接受单张 `input_reference`。插件必须支持 multipart decoder。反向代理或站点全局请求体配置如果更小，继续按更小限制执行。文件先流式写入立即 unlink 的临时描述符，读取完成后释放临时 32 MiB MIME 解析预算，失败/拒绝/取消立即清理孤儿文件并归还上传槽；通用模型读取器只读取最多 1 MiB 的文本 JSON，不缓冲整个图片。后台使用持久文件快照，不依赖该临时描述符。原生提交入口同时也支持预上传的 JSON `file_references`（先于请求插件、选路与上游 I/O 校验 user/Key/分组归属并向请求插件投影只读附件元数据）。

```sh
curl "$SITE/v1/videos" \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Idempotency-Key: unique-video-request' \
  -F 'model=sora-2' -F 'prompt=A quiet ocean' -F 'seconds=8' \
  -F 'input_reference=@reference.png'
```

大图和多素材幂等身份使用实际内容 SHA256、字节数、检测到的 MIME 和原始文本字段。改变 multipart boundary、不同名字段的排列或文件名不会重复创建任务，修改内容或同名文件内部顺序返回 409；单张小图 `input_reference` 沿用升级前 data URL 的指纹以支持旧任务重放。文件交给 decoder 前按字段名稳定排序，同名文件保留原顺序，因此哈希相同的上传具有相同执行语义。先检查已接受任务，再访问文件配额、当前插件和价格；重试只产生临时流式读写，不创建持久文件。失败或并发重放产生的未绑定素材在请求结束时删除，清理失败记录告警并由 1 小时过期回收。已绑定素材按保留策略保存。

直接上传复用四个上传槽，60 秒读截止时间，按共享 ingress 预算预留 32 MiB，覆盖文本表示及标准库 MIME header parser 的分配。标准上传不接受 Content-Encoding；单个文本字段最多 512 KiB，文本和归一化 JSON 各最多 1 MiB。匹配的 Task ingress 可以检查表单字段与本次上传的附件，文本字段可在估价前替换；文件只读，不进入 JS 正文。已经接受的同键重试优先返回原任务。

较大的素材通过以下**本站扩展**先上传、再引用。这些不是 OpenAI `/v1/files` 或官方 Videos 的新增字段。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| POST | `/v1/video-files` | raw body 上传；`Content-Type` 为 image/video/audio MIME，`X-Filename` 可选 |
| GET | `/v1/video-files/:id` | 查询归属于当前用户、Key、分组的元数据 |
| DELETE | `/v1/video-files/:id` | 删除尚未绑定任务的素材；已接受任务的素材由保留策略清理 |

例如：

```sh
curl "$SITE/v1/video-files" \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: image/png' -H 'X-Filename: reference.png' \
  --data-binary @reference.png
```

取响应 `id` 作为 `file_id`：

```json
{
  "model": "sora-2",
  "prompt": "A quiet ocean",
  "seconds": "8",
  "file_references": [
    {"file_id": "vfile_…", "field": "input_reference"}
  ]
}
```

`file_references` 最多接受 4 个绑定，保留每项 field；可以使用重复字段，例如 `image[]`，同一已上传 file_id 也可绑定不同角色或重复字段。不能与同名文本字段或直接文件混用，不能占用 model、prompt、metadata 等路由/文本保留字段。它向插件提供与 New API multipart 相同的 decoder body 和 `ctx.files`，因此插件必须支持 multipart 解码。独立的 ref 包含同名字段内序号，仍是 opaque 标识；单字段首个 ref 保持旧格式。数据库只为同一任务/文件保存一条保留关联，每个引用的元数据都重新核对。Sora 的大文件 multipart 已有真实 JS worker + 模拟 HTTP 验收；多文件经过专用测试插件验证，未修改原版 Sora 的单图限制；xAI 的小图 `__fileRef/dataUrl` 走有界内联。匹配的 Task ingress 会先按用户、Key、分组核对预上传归属，仅向插件展示只读元信息；文件 ID、正文及文件内容不进入完成报告。原有任务绑定事务仍重新校验保存的素材快照。声明视频/音频 MIME 不代表某个模型或插件支持该素材；官转五 1.1.0 的专用持久上传、分片及对象绑定见 `OFFICIAL_UPLOADS.md`；视频/音频还要求调用方提供已确认的素材时长。

## 持久性、权限与账务

- 文件元数据和内容保存在 PostgreSQL 私有表；内容每块最多 64 KiB。输入素材与视频成片分开，成片仍按原上游鉴权代理下载。没有新增公开桶或把输入素材写入公开图片 URL。
- 选择该实现是为了复用现有数据库的多节点与备份恢复语义，不依赖某台机器的本地路径或部署中尚未配置的对象存储。它适用于有界输入素材；后续高容量运营应把 blob 层换成私有对象存储，并单独验证生命周期。2 GiB 是原始素材字节上限，不是数据库实际磁盘、WAL 或备份空间上限。
- 上传先在 Unix 宿主创建 0600 临时文件并立即 unlink，读取/取消/崩溃后均由关闭文件描述符回收；慢客户端不持有数据库事务或锁。无法对打开文件 unlink 的宿主会明确拒绝上传。
- 文件归属固定为 user + API Key + group；Key 换组不能继续用于新请求。插件只能解析本任务保存的 ref，任意本地路径、其他文件 ID、其他用户/Key/分组均拒绝。
- 任务创建、文件绑定和资金冻结在原 `CreateHeld` 事务中提交。过期、身份不符或冻结失败全部回滚；并发幂等重放只绑定/冻结一次。重试已接受任务在文件读取和当前定价之前完成，因此素材后来过期也不产生新任务或收费。
- 未绑定素材保留 1 小时。绑定后至少保留 7 天；有未完成/未结算/待对账任务引用时继续保留，结束并结算后的最近更新时间重新起算 7 天。保留期间禁止手工提前删除。
- 每实例 1 个固定清理 worker，每分钟最多清理 20 个文件，10 秒预算，数据库行锁和 `SKIP LOCKED` 支持多节点；清理错误写固定日志。删除 chunk 与关联不删除任务、用量、价格、资金和事件依据。

## 固定资源边界

| 资源 | 当前上限 |
| --- | --- |
| 单文件 | 32 MiB；空文件拒绝 |
| 每用户 | 128 MiB / 64 个文件 |
| 全站 | 2 GiB / 4096 个文件；跨节点序列化准入 |
| 每实例并发上传 | 4；满载 503 + Retry-After |
| 上传时间 | 60 秒读截止时间及操作预算 |
| 上传内存准入 | raw 扩展 256 KiB；标准 multipart 32 MiB，使用既有共享 ingress budget |
| Task 文件快照 | 最多 4 条 / 16 KiB，重复字段具有不同 opaque ref |
| JSON 内联 | 单文件 512 KiB；最终正文最多 1 MiB |
| multipart 出站 | 最多 64 个 part / 约 129 MiB；Task 固定 worker 执行 |

用户/全站存储耗尽返回 429，正在上传的临时容量满载返回 503，文件过大返回 413；均不创建生成任务。网关或反向代理配置的请求体限制若更小，依然生效。

出站 multipart 在宿主临时文件中一次编码并计算实际 SHA256，随后 rewind、签名和发送相同字节。JSON `__fileRef` 仅允许基线规定的四个键，支持 base64/dataUrl 和可缩小 host 限制的 `maxBytes`；多层引用、总展开量、大小变化和 chunk SHA256 损坏均有检查。

## 尚未兼容的文件能力

这不代表真实厂商全覆盖：重复文件字段/多素材已接 `/v1/videos`、`/v1/tasks/:key` 与声明的原生 `meta.routes` / 根路径别名，Task JSON egress 已接允许内联的持久 `__fileRef` 附件工具、只读检查和单独授权的检查服务发送；复用真实文件 codec、归属和快照核对，原提交内容与签名不变，详见 `TRANSFORM_MEDIA.md`。客户端直接上传与 JSON `file_references` 均已接早/晚 ingress，发给上游的 multipart egress 仍按固定基线记录 unchecked。Provider Images（`/v1/images/edits`）与 Provider Audio（`/v1/audio/transcriptions`、`/v1/audio/translations`、`/v1/audio/speech`）已接有界 multipart（32 MiB / 64 part 上限、共享内存预留、拒接压缩 `Content-Encoding` 415，二进制音频响应附加 `X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer` 与 `Content-Security-Policy: sandbox`）。官转五 1.1.0 已接专用持久上传、断点恢复、对象绑定与失败清理；通用 OpenAI `/v1/files` 或任意上游对象号注册不属于本轮离线范围。不把离线模拟上传成功视为真实厂商兼容验收。

Task 成片的 `contentPath` 另已接入：按固定基线读取长任务 JSON 中的 Base64 字符串，验证后直接解码交付，GET/HEAD 支持，不持久保存成片。64 MiB 媒体 / 4/3 加 1 MiB JSON 上限、2 个并发槽和共享内存准入；内存中的 JSON 保留到交付关闭。取回请求仅 GET/POST JSON，不跟随操作重定向。准确语义见 `HOST_COMPATIBILITY.md`。

相关迁移：`264_generation_files.sql`。测试覆盖隔离 PostgreSQL 的持久读取、chunk 校验、归属、原子冻结/绑定、并发重放、取消、配额、保留清理，以及真实 worker 的 Sora multipart 和签名；未访问真实付费上游。
