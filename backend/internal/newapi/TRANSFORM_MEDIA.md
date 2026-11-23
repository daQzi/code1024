# 请求处理附件宿主

当前能力覆盖 New API 固定提交 `48f9df96ea7c68eae04a0f3687869f232f48929f` 的 JSON 内嵌媒体子集及视频客户端 multipart。字段形状、媒体优先于普通补丁、文本部分读取保护，以及 JPEG/PNG/WebP 重编码沿用其 `relay/requestplugin/{media,attachments,plan}.go`。这不等于完整文件与 multipart 兼容。

## 已接入

- 已支持的 JSON ingress/request/outbound/egress 为 data URL、Claude base64 source、Gemini inlineData / inline_data、Chat input_audio 生成 `[media:N]`、MIME 和大小投影；附件原始载荷不进入 JS 正文。
- Task JSON egress 现也识别持久 `__fileRef`，复用实际出站的文件 codec、每文件 512 KiB 和整体 1 MiB 有界准入。读取前核对当前任务 user/Key/分组及保存的大小、摘要和 MIME；插件只看到 `[media:N]`、原文件元信息或有限文字。两种 base64/dataUrl 编码均可检查、经单独授权向检查服务发送同次媒体；持久素材只读，不改写上传文件或实际提交内容。上游 multipart 和原始文本描述符明确记录 unchecked。
- `/v1/videos`、`/videos`、通用 `/v1/tasks/:key`（不含 `sunoapi`）与已声明 `meta.routes` 原生提交/根路径别名的客户端 multipart 早/晚 ingress 均使用已暂存的文件：`utils.attachments()` 返回字段、名称、类型、大小和只读 ref；`utils.media()` 有界检查图片头。附件数据不进入 JS 正文，不可改写或展开到 fetch。只有已存在的文本字段允许 `replace`，模型、流模式、文件引用和 JSON metadata 保持固定。JSON `file_references` 已接上述 Task 与原生路由的早/晚 ingress 元信息投影：先校验当前用户/Key/分组，插件只看附件类型、文件名、大小和角色，不接收原始 ID、读文件内容或重新绑定。
- `utils.attachments()` 按正文顺序返回内嵌附件及远程文件/图片引用；远程引用只展示元数据，没有可读取的本地 ref。`utils.media(ref)` 可读取类型、大小、文件名及可解码图片的尺寸。
- `utils.mediaText(ref, {maxChars})` 支持 UTF-8/带 BOM 的 UTF-16 文本、JSON/XML/YAML 等文本类型，以及 PDF、DOCX 文字。未知类型返回 `unsupported`，损坏/加密文档返回 `unreadable`，扫描 PDF 等无文字层文档返回 `no_text`，不执行 OCR。不存在的 ref 返回 null。
- 单份最多 128 Ki UTF-16 字符；同一 HTTP 请求的多个实例、早/晚 ingress 与出站阶段共用 512 Ki 提取限额和有界缓存，重复读取相同文件只计一次，缓存不跨请求。后台 Task egress 的同次执行共享独立缓存，不把前台提取正文持久化。每次 RPC 仍另有限制，不能靠重复读取放大回传文本。
- PDF/DOCX 不允许 `media.text` 改写；保留原二进制并记录 `not_rewritable`。文本类型同时依据 MIME 与文件扩展名判断，图片/音频/视频 MIME 不能被文本扩展名绕过。
- ingress / 晚 ingress / outbound 可使用 `media.text` 改写内嵌文本；仅读取一部分的文件不改写，保留原文并记录 `not_rewritable`。文本和模型等普通补丁作为一个整体校验，失败不会留下部分改写。
- 已支持的 HTTP outbound 可使用 `media.reencode`：auto、jpeg、png、webp、quality / maxSide。沿用原版缩放、透明图转 JPEG 白底、仅采纳变小结果、动画 GIF 保留、WebP 帧尺寸校验；同步更新 Gemini/Claude 的 MIME 字段。其他阶段不开放图片重编码。
- 已授权 ingress/egress `utils.fetch` 的 JSON 正文可以精确引用 `[media:N]`；由宿主拼接流式 data URL。最多 8 个引用、展开正文 32 MiB，仍受每 hook 四次、响应 1 MiB、整个阶段时限及公网/允许主机/禁重定向限制。默认禁网、试运行禁网，不自动附加账号或客户端凭据。
- 执行记录和后台页面显示检查/截断/无法改写状态，以及图片 MIME、大小和有损/无损编码变化。每条最多 16 个附件摘要，其余仅记数量。记录没有文件名、地址、路径、正文或载荷。

## 进程与资源边界

真实附件数据保留在 Go 宿主；子进程只收到投影、按需元数据和有限文本。`io.Reader` 不做 JSON 序列化；同一 hook 的 fetch 回调在宿主使用本次 Source 创建 reader，不接受插件指定本地路径或其他请求的文件标识。媒体回调最多 128 次，RPC 返回文本累计最多 512 Ki UTF-16 字符，出错/取消关闭子进程和实际 HTTP I/O，不留下持有请求的后台 goroutine。

当前源 JSON 最多 1 MiB、8192 节点、64 层、64 个附件；输出补丁继续服从原有 1 MiB、保护路由/计费字段和原子性检查。图片在解码像素之前核验尺寸（最多 4000 万像素）和内存准入。重编码最多两个并发，同时占用请求共享预算和独立 512 MiB 进程预算；像素、缩放、透明底和 codec 工作区按保守上限预留。输出 writer 校验取消和 1 MiB 上限，释放时归还准入。宿主容量不足返回 capacity，不通过零值或绕开预算继续执行。

PDF/DOCX 解析直接移植固定基线的有界读取器，并额外响应调用者取消。每份解码文档不超过 10 MiB（当前内嵌 JSON 上限更小），最多两份并发，等待最多 5 秒、解析最多 3 秒；解析前从请求和进程预算各预留 160 MiB，容量不足明确返回 `busy`。PDF 保留原版对象/xref/压缩流/字体/CMap/页数/嵌套限制，DOCX 只读取 ZIP 内的 `word/document.xml`，解压 XML 最多 32 MiB、嵌套最多 256 层，重复主文档拒绝。正文留在宿主，记录不保存文件名、地址或提取文字。

## 尚未包含

- 通用 `/v1/files` 文件代理路由；Provider Images/Audio 的有界 multipart/二进制转发与 Responses WebSocket 已接入各自专用网关通道，不经文本媒体 Transform 改写二进制载荷。
- Task JSON 中预上传引用的内容检查和更大的持久素材检查；当前持久文件接点仍限允许内联的 Task JSON egress。
- 远程引用下载、OCR、旧版 DOC 和其他 Office 格式、任意本地文件读取。
- 官转五真实上游存储与对象生命周期验收（离线分片/上传/对象绑定已实现，真实联调随 R8）。
- report / reply 的请求附件 Source：报告继续使用私有快照的有限投影，回复接口不因此获得输入文件。

## 插件示例

下面的 outbound 示例按内嵌图片索引请求优化，执行仍需配置对应 HTTP 实例：

```js
export function outbound(ctx, req) {
  const effects = (req.media || [])
    .filter(item => item.mimeType.startsWith("image/"))
    .map(item => ({
      type: "media.reencode", media: item.index,
      format: "webp", maxSide: 1024
    }));
  return { action: "continue", effects };
}
```

源码安装不自动获得网络授权；网络开关和固定版本继续由管理员管理。
