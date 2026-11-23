# 官转五持久素材上传

这是本站 `sub2api-official5` **1.1.0** 的宿主扩展，不是 New API 固定基线新增的通用 JS 上传 API。原版 17 个脚本不改动；1.0.0 已安装版本及账号固定 revision 不自动迁移。

## 公开合同依据

2026-11-22 核对用户给出的公开文档 <https://sub2api.0x0.fan/official-5/api>，以及同站公开客户端 `videoGenerations-BNKOoQZ6-mwr481ht.js`、`LongStorageLibraryModal.vue_vue_type_script_setup_true_lang-BjPwD8KV-mwr481ht.js`。

- `POST /api/v1/video-generations/uploads` 提交 SHA256、字节数、kind、Content-Type、稳定幂等号；视频/音频另传调用方确认的时长，宿主不猜测。
- `owner.upload` 为 `session_token/generation/mode`；multipart 的 `parts[].part_number/size_bytes` 按编号确定切片，连续编号必须从 1 开始，合计必须正好覆盖文件。最多 128 片。
- `waiting` 使用 ticket 调用 `/wait` 并遵守有界 retry_after_seconds；`reused.reference` 和 `/complete` 的结果均必须匹配 kind、SHA256 和 size_bytes，才能接受其 storage_object_id。
- 本站只使用同上游 `/uploads/relay`：single 使用 part 0，multipart 使用实际编号。relay 返回 data.etag 用于完成校验；忽略所有返回的存储 URL，不跟随重定向。
- `POST /complete` 提交会话身份和 multipart ETag 列表；`POST /abort` 清理未完成上传。没有已完成对象删除的公开合同，不猜测或调用删除接口。

上游文档会变化；以上客户端构建标识是本次依据，真实厂商存储及计费行为仍需受限联调。

## 持久执行

1. 直接 multipart / 先上传引用由既有私有文件宿主接收。decoder 只见 opaque ref 和元信息。`input_reference` 图片以及 reference_image/video/audio、image[]/video[]/audio[] 支持最多 4 个绑定。
2. 新任务资金冻结与 `preparation_status=pending` 同事务。准备阶段从未发送付费生成，租约失联可恢复；其他旧任务仍保留提交租约失联即人工对账的行为。
3. Go 宿主每次只打开本任务归属的文件，核对 user/Key/group、快照和实际 SHA256。文件流入立即 unlink 的私有 spool，最多 32 MiB/文件，不整体进入 JS；只有已有的两个 Task worker 执行准备。
4. 原上传凭据、原账号、原连接、固定版本、请求和文件身份、上传会话与逐片进度用既有 AES 加密器保存，最大 64 KiB 明文/96 KiB 密文。每个检查点必须仍持有有效任务租约。先保存 owner 再发字节，逐片保存 ETag，single 上传成功标记也先保存再 complete。
5. complete 响应丢失后恢复只重试 complete，不重传已确认片/单文件。准备全部完成后持久切换 ready，下一次认领才允许生成提交。上传、完成、检查点重放都不执行生成或二次冻结。
6. 生成提交前用已绑定对象替换内部 file_ref；签名和 egress 使用最终实际 JSON。换凭据、换请求/文件身份、来源脚本冒领、未完成准备或缺少对象一律在付费请求前失败。返回内容与日志不公开上传 session、上游 Key 或本地文件路径。

文件上传会有上游存储/网络成本，本实现没有把它猜成独立下游收费项。生成计费继续使用原固定单价/最终账单规则。

## 失败与清理

- 临时网络/等待在 15 分钟内继续同一准备；取消关闭当前真实 I/O，超时后可从持久检查点继续。
- 永久拒绝、完整性错误、凭据变化或重试超时使任务失败并进入原有全额释放流程。资金不等待上游存储恢复。
- 结算后独立 1 分钟 worker 认领 cleanup，每次最多一个任务、90 秒、两分钟数据库租约；重启或 abort 失败继续重试。使用检查点里加密的原凭据，只能访问原上游的固定上传路径，即使账号已换 Key/删除仍可尝试清理。
- abort 成功逐项保存，全部完成后清空加密状态。正常已准备的任务在资金结算事务内清除上传凭据；不清除账本、任务、文件快照或上游生成身份。
- 已完成对象可能依上游保留规则继续存在。无法解密、上游永久拒绝旧凭据等问题会保留 cleanup 依据，记录无敏感内容的固定告警；不因清理失败追回已退款项。

## 与媒体检查的边界

内部 file_ref 是上传准备指令，不是 Base64/JSON 内联 `__fileRef`。生成阶段的 JSON egress 读取最终上游对象引用；它不能据此宣称检查过上传二进制。当前固定基线的直接 multipart 仍按 unchecked 记录。Provider multipart 和其他持久文件 Transform 接点继续列在剩余清单。

## 升级与回退

先应用迁移 `287_generation_preparation.sql`，再启动新二进制；工厂注册新增 1.1.0 固定版本，管理员按需更新账号。旧任务字段默认空，保持旧行为。

不能让旧二进制处理新的 pending/ready 任务：旧版本不知道上传准备边界。回退前停止新视频接入及所有 Task worker，排空或人工核对 1.1.0 在途任务、完成上传清理，再禁用该 revision 并恢复旧账号固定版本；保留表和检查点以供恢复。不得直接删除加密状态、伪造已准备或把结果不明的生成改回队列。真实升级/回退演练仍在 R8。
