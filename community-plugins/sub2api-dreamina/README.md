# `community-sub2api-dreamina`（官转二 · 独立社区插件）

- **Plugin Key**: `community-sub2api-dreamina`
- **Version**: `1.0.0`
- **License**: `AGPL-3.0-or-later`
- **Plugin SHA256 (`plugin.js`)**: `5086be00e4359bab23bbabdeeeabeedba52fa0bf114149f1501dd9a85b6981e4`
- **Upstream Contract**: <https://sub2api.0x0.fan/dreamina/api>（基于 2026-11-21 公开文档）

## 能力与模型范围

- 支持协议：`openai_video`（`/v1/videos` 创建、查询、列表及 `GET`/`HEAD` 鉴权下载）
- 支持模型：
  - `seedance-2.0`（4–15s，720p/1080p/4k）
  - `seedance-2.0-fast`、`seedance-2.0-mini`（4–15s，720p）
  - `seedance-2.5-draft`、`dreamina-seedance-2.5-draft`（4–30s，480p）
  - `seedance-2.5-flash`、`dreamina-seedance-2.5-flash`（4–30s，480p/720p）
  - `seedance-pro-fast`（5–10s，720p/1080p，仅文生和首帧）
- 支持任务模式：`text`、`first_frame`、`first_last_frame`、`references`
- 支持素材来源：`https://...` 公网 HTTPS URL 或上游 `storage_object_id`（按各型号数量上限校验，拒绝 `file://`、内联 Base64 data URL 及无图纯音频组合）
- 计费与对账：`usageSchema.seconds` + `extractUsageOnComplete` 仅在 `billing_status === "billed"` 且 `final_cost` 为非负数值时输出 `upstream_cost`（默认 `CNY`），否则保持 `billing_pending: true` 直到上游结算或超窗转人工核对。

## 账号配置与定价示例

1. 通过插件中心安装本目录 [`plugin.js`](plugin.js) 并启用版本（投稿社区市场时请将 `meta.key` 替换为页面提示的开发者命名空间 key）。
2. 新建或编辑普通 `openai` API Key 账号：
   - **Base URL**: 目标站点根地址（例如 `https://sub2api.0x0.fan`，插件自动拼接 `/api/v1/video-generations`）
   - **API Key**: 对应渠道分组的上游 Key
   - **模型映射**: 例如 `{"seedance-2.0": "seedance-2.0"}`
3. 最终账单加价（`cost_plus`）定价示例（汇率与冻结上限请按实际配置）：

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
GOWORK=off go run ./cmd/pluginctl lint ../community-plugins/sub2api-dreamina/plugin.js
GOWORK=off go run ./cmd/pluginctl test ../community-plugins/sub2api-dreamina/plugin.js --fixture ../community-plugins/sub2api-dreamina/fixture.json
```

> 说明：项目内置的 `sub2api-dreamina` 保持原样未修改，以保障既有绑定账号与历史任务稳定可读；本包可独立分发与上架，互不覆盖。
