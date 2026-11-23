# 官转二 / 官转五视频插件

这两个独立的 JS 插件把本站 `/v1/videos` 转为上游 `/api/v1/video-generations`。
实现依据为 2026-11-21 用户提供的公开文档：

- 官转二：<https://sub2api.0x0.fan/dreamina/api>
- 官转五：<https://sub2api.0x0.fan/official-5/api>

## 管理员配置

1. 在插件管理安装内置 `sub2api-dreamina` 或 `sub2api-official5`，启用其版本；或者从独立插件开发仓库 `../code1024-plugins` 安装社区插件包 `community-sub2api-dreamina`（1.0.0） / `community-sub2api-official5`（1.1.0）。**内置版本源码及 SHA256 保持原样**，原有绑定账号与历史任务不受影响，官转五宿主分片上传准备工作流继续仅针对内置 `sub2api-official5@1.1.0` 生效。
2. 在账号中添加普通 API Key 账号，填站点根地址（例如 `https://sub2api.0x0.fan`），选择插件固定版本，填写对应渠道分组的上游 Key。
3. 绑定本站分组、并发和公开模型映射。官转五必须从该 Key 的能力接口或 `/v1/models` 获取真实型号，显式配置 `公开模型名 → cc-seedance-实际型号`。`cc-seedance-*` 是插件家族声明，不能作为可调用的模型名或展示型号。
4. 设置售价；账号未设置时沿用分组视频价格。上游连接仍使用账号管理，无需另建连接记录。

官转二声明：`seedance-2.0`、`seedance-2.0-fast`、`seedance-2.0-mini`、`seedance-2.5-draft`、`seedance-2.5-flash`、`seedance-pro-fast`，及文档明确的两个 `dreamina-seedance-2.5-*` 别名。
模型、规格、参考限制最终以上游当前能力及 Key 权限为准。插件不会修改账号分组或猜测新增型号。

## 下游示例

使用本站 Key 与公开模型名。`Idempotency-Key` 在同一任务重试时保持一致。

```bash
curl https://YOUR_SITE/v1/videos \
  -H 'Authorization: Bearer YOUR_SITE_KEY' \
  -H 'Idempotency-Key: video-order-001' \
  -H 'Content-Type: application/json' \
  -d '{"model":"YOUR_PUBLIC_MODEL","prompt":"海边日落，镜头缓慢推进","seconds":"8","size":"1280x720"}'
```

查询、列表和下载均使用本站返回的 `video_...` 编号：

```text
GET /v1/videos/{id}
GET /v1/videos
GET /v1/videos/{id}/content
HEAD /v1/videos/{id}/content
```

标准字段包括 `id/object/model/status/progress/seconds/size/prompt/completed_at/error`。
额外的 `billing_status` 与 `delivery_status` 用于区分成片可下载、费用待定。
下载传 `Range` 可断点续传。上游重定向由宿主受限跟随，凭据不会发给存储站点。

`size` 支持常用 480p/720p/1080p/4k 横竖屏以及 720p 的 1:1、4:3、3:4；模型自身仍须允许该规格。
其他原生规格可省略 size，使用 `metadata.resolution` 与 `metadata.ratio`，两处配置不能冲突。

## 参考素材

两者都支持通过 `metadata.content` 保留素材顺序：

```json
{
  "model": "YOUR_PUBLIC_MODEL",
  "prompt": "参考 [@Image 1] 中的场景",
  "seconds": "8",
  "metadata": {
    "resolution": "720p",
    "ratio": "16:9",
    "task_mode": "references",
    "content": [
      {"type":"image_url","role":"reference_image","storage_object_id":"UPSTREAM_OBJECT_ID"}
    ]
  }
}
```

- 官转二支持 `image_url/video_url/audio_url`，可使用 `url` 公网 HTTPS 链接，或已上传对象编号。单图首帧也可用 `input_reference: "https://..."`。首尾帧用 `task_mode=first_last_frame`，按 `first_frame`、`last_frame` 顺序提供两张图片；pro-fast 只支持文生和首帧。
- 官转五只支持 `text/references`，自动补 `provider_mode=standard`。参考对象必须已上传到该上游 Key 可访问的存储，拒绝 URL、本地路径、Base64 和不受支持的参数；`input_reference` 可传 `{"storage_object_id":"..."}`。
- 官转五 **1.1.0** 支持本站持久文件和标准 multipart 的自动上传：申请、等待/复用、single/multipart 中转、ETag 检查、完成和对象绑定均在 Go 宿主执行。文件不进入 JS 内存；只访问任务固定的上游 `/uploads` 系列路径，忽略云存储 URL，不将上游 Key 发到存储地址。旧账号继续固定其旧版本，管理员需显式选用新版本。
- 直接提供 `storage_object_id` 时，仍须调用方保证它属于该上游 Key，本站不能凭对象字符串证明外部归属。不能混用其他账号或其他上游对象；多账号有不同对象权限时，应以独立公开模型限制路由。自动上传产生的对象会与任务、原账号、固定版本、连接、凭据及文件 SHA256 绑定；换 Key 后不会拿旧对象创建新生成。
- 这里只适配视频生成，不开放图片、音频、取消、扩展、编辑或分镜下载的新路径。

标准单图上传：

```sh
curl "$SITE/v1/videos" -H "Authorization: Bearer $API_KEY" \
  -H 'Idempotency-Key: stable-video-order' \
  -F 'model=YOUR_PUBLIC_MODEL' -F 'prompt=海边日落' -F 'seconds=8' \
  -F 'input_reference=@reference.png'
```

多素材还可使用 `reference_image` / `image[]`、`reference_video` / `video[]`、`reference_audio` / `audio[]`，最多 4 个文件。同名字段顺序保留；不同字段按本站持久快照的稳定字段顺序。视频和音频必须在 `metadata.reference_durations` 中按字段填写调用方确认的秒数，重复字段用数组，例如 `{"reference_durations":{"video[]":[2.75],"audio[]":[4.2]}}`。宿主不猜测或伪造时长，上游继续校验实际媒体。文件与现有对象不能在同一请求混用；先上传/引用扩展详见 `FILE_HOST.md`。

JSON 现有对象支持 `client_ref_id` 与 `metadata.selected_reference_ids`，编号须在任务内唯一且引用已声明编号。原素材顺序不因选择编号改变。

上传占用可恢复的准备阶段，不占 JS worker，也不创建付费生成；上传会话与逐片进度加密检查点先落库，失联后按原幂等号继续，完成响应丢失只重试完成。成功准备持久提交后才进入原有生成提交边界；越过该边界的结果不明仍转人工核对，不自动重发。准备失败最多重试 15 分钟，释放资金后由独立有界 worker 重试 abort，清理失败不阻塞退款；上游没有公开已完成对象删除合同，已完成对象由其存储保留规则管理。迁移和回退限制见 `OFFICIAL_UPLOADS.md`。

## 两种定价

固定价可以按任务或请求视频秒数收费，原有按 Token、积分的插件继续可用。
成本加价有两种方式：管理员配置上游单价 × 最终用量，或者直接读取上游最终账单。

最终账单模式的账号定价示例（示例汇率和上限请按实际情况设置）：

```json
{
  "*": {
    "mode": "cost_plus",
    "source_unit": "upstream_cost",
    "source_currency": "CNY",
    "exchange_rate": "0.14",
    "markup": "1.2",
    "service_fee": "0",
    "minimum": "0",
    "maximum": "10"
  }
}
```

- `maximum` 为应用倍率前的 USD 冻结上限，必填且必须大于零。用户余额或订阅按分组倍率预留，账号额度按账号倍率独立预留。该上限不是上游真实报价。
- 售价 = max(最低收费, 上游 final_cost × 汇率 × 加价倍率 + 服务费) × 分组倍率。
- 币种、加价和规则随任务保存；改价不影响在途任务。上游显式返回其他币种时按不匹配转人工核对。
- 只在 `billing_status=billed` 且 `final_cost` 是有效非负金额时确认成本。最终零费用有效，缺失或冻结期的 0 不代表免费。
- 不自动调用可能耗时 150 秒的官网报价接口。若希望细化按规格的售前报价，应另做持久报价宿主，不能把历史起价当最终成本。
- 完成后从保存的最终账单和冻结规则重算实际费用。缺失、为 null、币种错误或超上限时进入人工对账；不会补零或追加扣余额。结算与差额退款沿用原账本事务；分组倍率为零时，账号额度仍按实际成本和原预留比例独立结算。

## 状态与边界

- HTTP 202/code=0 只代表上游入队；排队、积分等待与保存成片继续查询原任务。
- 本站用持久任务 ID 作为上游 `X-Request-ID`。网络结果不明时进入待对账，避免自动重复生成；人工确认上游编号可恢复查询。
- 成片就绪可下载，账单冻结时后台继续只查询原任务；生成完成 2 小时仍无有效最终账单转人工对账。
- 官转五只返回分镜时，任务保留终态及最终费用，但不声明完整 video 产物；查询提示 `final_video_unavailable`，完整视频下载返回不可用。当前不自动合成或提供分镜下载端点。
- 成片仍由上游保存与代理下载，本站没有永久转储保障。
- 本轮验证使用本地模拟上游、真实插件子进程、`pluginctl` 离线回放与隔离 PostgreSQL。真实上游 Key、付费生成和厂商实际存储联调仍待验收。

## 独立社区插件项目

社区插件的独立开发与发行目录已迁移至与本仓库同级的独立项目 `../code1024-plugins/`（内置标准开发文档、`tools/plugindev`、`Makefile` 及外部市场 `index.json`）：

- `../code1024-plugins/plugins/tasks/community-sub2api-dreamina/1.0.0/`：Key `community-sub2api-dreamina`（`1.0.0`，31/31 fixture 校验通过）
- `../code1024-plugins/plugins/tasks/community-sub2api-official5/1.1.0/`：Key `community-sub2api-official5`（`1.1.0`，20/20 fixture 校验通过）
