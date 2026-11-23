# `community-sub2api-official5`（官转五 · 独立社区插件）

- **Plugin Key**: `community-sub2api-official5`
- **Version**: `1.1.0`
- **License**: `AGPL-3.0-or-later`
- **Plugin SHA256 (`plugin.js`)**: `7dba6a094fc9e2807370005a8c27f4621f818e60b155338d8fb46a0728886cb9`
- **Upstream Contract**: <https://sub2api.0x0.fan/official-5/api>（基于 2026-11-21 公开文档）

## 能力与模型范围

- 支持协议：`openai_video`（`/v1/videos` 创建、查询、列表及 `GET`/`HEAD` 鉴权下载）
- 模型族：`cc-seedance-*`（正则表达式 `^cc-seedance-[a-zA-Z0-9._-]+$`，**必须**在账号配置中设置显式模型映射，例如 `{"my-official5-video": "cc-seedance-2.0-fast"}`，不可直接把通配符当作公开模型调用）
- 支持任务模式：`text` 与 `references`（提交自动附带 `provider_mode: "standard"`；拒绝 `first_frame`/`first_last_frame`）
- 素材与分段交付：
  - JSON 请求仅接受已上传到对应上游 Key 下的 `storage_object_id`（支持 `client_ref_id` 与 `metadata.selected_reference_ids` 唯一筛选）；拒绝公网 URL 与内联 Base64。
  - 若经支持 bound files 的宿主解码 `multipart` 请求，要求在 `metadata.reference_durations` 为视频/音频素材提供经调用方确认的实测秒数。
  - 若上游返回 `delivery_kind: "segment"`，任务终态后 `listArtifacts` 与 `buildContentRequest` 拒绝下载片段，并由 `protocols.openai_video.render` 返回 `final_video_unavailable` 提示人工核对交付。
- 计费与对账：`usageSchema.seconds` + `extractUsageOnComplete` 仅在 `billing_status === "billed"` 且 `final_cost` 为非负数值时输出 `upstream_cost`（默认 `CNY`），否则维持 `billing_pending: true`。

## 与内置 `sub2api-official5@1.1.0` 宿主分片上传的边界说明

- Sub2API 宿主中的“官转五 1.1.0 专用持久素材分片上传准备”（`backend/internal/service/generation_preparation.go` 与 `internal/newapi/officialupload`）出于安全边界，按内置 `sub2api-official5` 1.1.0 的精确源码与 SHA256 进行校验并保留内置版本不变。
- 因此，若使用**独立社区版** `community-sub2api-official5` 安装或投稿，请使用文生视频或先在上游获取的 `storage_object_id`；如需宿主代传直传 multipart/预上传素材到官转五上游存储，请直接在账号中选用内置固定的 `sub2api-official5@1.1.0`。两者互不冲突、也不影响旧账号或历史任务。

## 账号配置与定价示例

1. 通过插件中心安装本目录 [`plugin.js`](plugin.js) 并启用版本（投稿社区市场时请将 `meta.key` 替换为开发者命名空间 key）。
2. 在普通 `openai` API Key 账号中填写 Base URL（如 `https://sub2api.0x0.fan`）、上游 Key 以及显式模型映射（不可省略 `public -> cc-seedance-*` 映射）。
3. 最终账单加价（`cost_plus`）定价示例：

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

## 离线验证

```sh
cd newapi-engine
GOWORK=off go run ./cmd/pluginctl lint ../community-plugins/sub2api-official5/plugin.js
GOWORK=off go run ./cmd/pluginctl test ../community-plugins/sub2api-official5/plugin.js --fixture ../community-plugins/sub2api-official5/fixture.json
```
