# 官转二 / 官转五视频插件

这两个独立的 JS 插件把本站 `/v1/videos` 转为上游 `/api/v1/video-generations`。
实现依据为 2026-11-21 用户提供的公开文档：

- 官转二：<https://sub2api.0x0.fan/dreamina/api>
- 官转五：<https://sub2api.0x0.fan/official-5/api>

## 管理员配置

1. 在插件管理安装内置 `sub2api-dreamina` 或 `sub2api-official5`，启用其版本。
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
- 本站目前没有代理上游的上传申请、分片、完成与等待接口，也没有把 OpenAI 文件引用自动转为上游存储对象。需要调用方/管理员先按上游文档完成上传。不能将本站其他账号或其他上游的对象编号混用。多账号有不同对象权限时，应使用独立公开模型映射固定路由范围。
- 这里只适配视频生成，不开放图片、音频、取消、扩展、编辑或分镜下载的新路径。

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

- `maximum` 为加本站分组倍率前的 USD 最大冻结额，必填且必须大于零。该金额是管理员授权的上限，不是上游真实报价。
- 售价 = max(最低收费, 上游 final_cost × 汇率 × 加价倍率 + 服务费) × 分组倍率。
- 币种、加价和规则随任务保存；改价不影响在途任务。上游显式返回其他币种时按不匹配转人工核对。
- 只在 `billing_status=billed` 且 `final_cost` 是有效非负金额时确认成本。最终零费用有效，缺失或冻结期的 0 不代表免费。
- 不自动调用可能耗时 150 秒的官网报价接口。若希望细化按规格的售前报价，应另做持久报价宿主，不能把历史起价当最终成本。
- 上游费用超过冻结额时进入人工对账，不追加扣余额。结算与差额退款走现有持久资金事务，不因重复查询重复扣费。

## 状态与边界

- HTTP 202/code=0 只代表上游入队；排队、积分等待与保存成片继续查询原任务。
- 本站用持久任务 ID 作为上游 `X-Request-ID`。网络结果不明时进入待对账，避免自动重复生成；人工确认上游编号可恢复查询。
- 成片就绪可下载，账单冻结时后台继续只查询原任务；生成完成 2 小时仍无有效最终账单转人工对账。
- 官转五只返回分镜时，任务保留终态及最终费用，但不声明完整 video 产物；查询提示 `final_video_unavailable`，完整视频下载返回不可用。当前不自动合成或提供分镜下载端点。
- 成片仍由上游保存与代理下载，本站没有永久转储保障。
- 本轮验证使用本地模拟上游和真实插件子进程。真实上游 Key、付费生成与完整上传流程没有在本轮验证。
