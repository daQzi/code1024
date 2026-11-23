# Provider 工具计费

固定参考：New API `48f9df96ea7c68eae04a0f3687869f232f48929f` 的工具计数、
逐模型工具价格查找和 Provider hook 覆盖语义。独立实现位于 `newapi-engine/provider`；
账号、售价持久化、权限和唯一用量账本由本站提供。

## 管理员配置

在「插件中心 → Provider 工具售价」读取并编辑 JSON 价格表。单位统一为 **USD/千次**。
支持默认工具名和 `工具名:模型前缀*`，如 `file_search`、`file_search:vendor-mini*`。
最长模型前缀优先，再取工具默认价；与固定基线相同，省略尾部 `*` 仍表示前缀匹配。
模型匹配区分大小写。相同工具的等价前缀不能重复声明。

- 数字 `0` 是明确免费，命中后停止回退。字符串、null、负数、NaN/Infinity 不合法。
- 全表最多 512 项，工具名最多 128 字节，完整 key 最多 384 字节；售价上限为
  `1e12` USD/千次，这是计算边界，不是推荐价格。
- 价格表优先于旧分组搜索价。未命中时，仅 web_search / web_search_preview 可以回退到
  当前分组**显式配置**的搜索价；其他正数用量必须配置价格。
- 空对象移除全部覆盖，不表示所有工具免费；既有明确分组搜索价仍可使用。
- 不导入厂商默认价格、不混用上游列表货币，也不自动转换汇率。自动上游报价、成本价和差价
  仍是独立的后续工作，不能把本表当作上游采购成本。

API 为 `GET /api/v1/admin/newapi-plugins/provider-tool-prices` 和同地址 `PUT`。
保存使用 `{prices, revision}` 完整替换，必须经过管理员权限和二次验证；旧 revision 或并发
修改返回 409。页面保留冲突草稿，刷新前应复制并核对。售价表沿用 settings 表 CAS，
本表无新增迁移；历史明细使用下述迁移 288，不新增钱包。

每节点最多缓存 2 秒，保存后本节点立即失效并回读；其他节点在下次缓存到期时使用新值。
启动和刷新均校验配置，错误不恢复陈旧/免费默认值。每个请求在出站前固定不可变价格表、
计费模型名和分组搜索价；后续保存不改变在途请求。

## 插件费用事实

`convertResponse` / `convertStreamEvent` 可返回 `tools: {工具名: 次数}`：

- 每次 hook 返回本次增量；JSON next 链、流事件和实际执行到的 EOF 共用一个请求累计器。
- 单 key 最多 10000 次，请求内最多 64 个 key，非法批次不应用一半。
- 凭据拒绝、明确上游错误和未执行的 EOF 不增加事实。不要在每次轮询重复报告历史累计值。
- HTTP 401 或 `credentialRejected` 确认拒绝时，该步的 `tools/next/state` 全部丢弃；
  即使插件返回合法 next，仍只能由宿主续期重试当前步骤或按原错误退出，不能跳到下一步。
- hook 的某 key 最终累计值覆盖同 key 的标准协议推断，包含明确的 `0`；没有报告的其他
  协议 key 保留。插件既可报告 hosted 工具，也可报告已配置价格的自定义厂商工具。
- 上游没有返回的调用次数不能由模型名称、工具声明或估计补造。

目前公开 Chat/Responses wire 还会观察实际调用：Responses 的 web_search_call、
file_search_call 和已明确配置正价格的自定义 function_call；Chat 的已定价自定义函数调用。
Responses 过程事件与最终 output 快照按工具、位置和 ID 去重；客户端函数与保留 hosted
工具同名时不会被误算成 hosted 调用。单请求最多观察 10000 个实际调用及有界身份。
另外已接入以下真实上游字段，优先级为 **hook 明确计数 → 厂商累计报告 → 公开协议调用**：

- Anthropic 预置及其 `+plugin` 组合：JSON `usage.server_tool_use`、SSE
  `message_start.message.usage` / `message_delta.usage` 的 `server_tool_use`，分别映射
  `web_search_requests`、`web_fetch_requests`、`code_execution_requests`、`tool_search_requests`
  到 `web_search`、`web_fetch`、`code_execution`、`tool_search`。
- OpenAI 兼容 wire（含预置转换前）：xAI `usage.server_side_tool_usage_details` 及 Responses
  终态的 `response.usage`，把 `web_search_calls`、`x_posts_fetched`、`x_users_fetched`
  分别映射到 `web_search`、`x_search_posts`、`x_search_profiles`；收到真实搜索报告时，
  同时将 `web_search_preview` 的推断置零，不能两个别名都收费。`num_sources_used` 不算调用数。
- 阿里 Responses `usage.x_tools.web_search.count` 计入 `search_strategy_agent`；Azure
  Responses `tool_usage.web_search.num_requests` 计入 `bing_web_search`，终态均可位于
  `response` 下。二者收到真实报告时将 OpenAI 的两个搜索别名置零，沿自己的显式售价收费。
  同一响应出现冲突搜索报告、同请求中途改换这类搜索计量来源时明确失败，不把同次搜索收两遍。
- Gemini 预置及其 `+plugin` 组合：对所有 candidate、所有流事件的 `groundingMetadata`
  中 `webSearchQueries` / `imageSearchQueries` 去空白、去空、合并去重，分别记录
  `google_search` 的不同查询数与 `google_search_grounded_prompt` 的 1 次。这与固定基线一致，
  是返回查询的证据统计，**不是厂商保证的最终收费次数**；来源链接、retrievalQueries 和工具声明不算。
  转成 Responses 时产生的搜索展示项不再按 OpenAI 搜索收费，防止同一证据收两次。

Gemini 的两种指标都须有明确价格；不计费的指标请配置 `0`，宿主不会按模型名称猜价格规则。
例如站点选择按查询收费，就给 `google_search` 配置自己的 USD/千次售价，并将同模型的
`google_search_grounded_prompt` 设为 `0`；按 prompt 收费则相反。二者也支持最长模型前缀。
只有真实非空查询才产生计数；每请求最多 10000 个不同查询，仅在内存保留 SHA-256 去重值，
不把查询原文或摘要写入工具费用快照。

厂商报告使用累计替换，不逐帧相加；字段缺失保留之前事实，显式零覆盖，失败/错误事件不增加用量。
有歧义的计费字段、非整数、负数、超限、非有限值明确失败，不截断或当零。与固定基线较宽松的
DTO 解码不同，这是本站对账务事实的额外校验。转换前保存受支持字段，`+plugin` 须先还原预置协议；
若自定义转换已删去私有字段，插件应通过 `tools` 报告，宿主不会读取不明 envelope 或按域名/模型猜测。
其他厂商、厂商专属搜索计价 key 仍需各自适配；未自动识别的事实继续由 hooks 报告。
例如 DeepSeek Search/Fetch 的分项和 GLM 的 search_engine 需要确定的渠道/请求语义，
仅凭公共 web_search_call、结果数组或模型名不能安全选定它们的售价 key；插件作者应在
`tools` 中报告实际分项，并显式将被替代的公开搜索 key 报为零，不能只增加新 key 导致重复收费。

明确请求 hosted search/file_search 但缺价，会在令牌交换和普通上游 HTTP 前拒绝。
响应才出现的未定价正数也会报错，不偷偷采用其他厂商的默认价。流式的未知费用可能在输出
部分内容后才发现；保留已确认 token/已定价工具费用，未定价部分不猜测，不把该请求标为成功。

## 账本与边界

工具基价按实际次数 / 1000 × 固定售价计算，与 token 加价合并后经过本站既有基础分组倍率，
通过同一次 `RecordUsage` 和钱包事务记账；兼容展示用 SearchCount 不会再收一次搜索费。
token 定价失败仍遵循原网关错误合同，不以工具费用伪装 token 已正确计价。

迁移 `288_usage_log_provider_tool_billing.sql` 为普通用量表增加 nullable JSONB
`provider_tool_billing`。版本 1 保存每项工具的实际次数、当时命中的 USD/千次售价、倍率前金额、
基础工具倍率及已包含的工具费用；不套用 token 的高峰倍率，不在展示时重新查询当前价格。
未配置且为零次的工具单价保持未知，明确免费价格和零倍率保持真实的零。

这份快照与原用量记录同次写入，重复请求保留原记录；它是合并费用的组成说明，不是额外扣费、
独立钱包、账本扣款成功凭据或上游采购成本。原 token 定价失败回退时不记录虚假的已计费工具明细。
管理员和用户用量页共用「工具费用明细」，只展示已知版本；历史 NULL 不回填、不按今天价格倒算。
新增列可空，旧二进制仍可按原显式列写入，回退时应保留已记录的明细。

视频 Task 的冻结、结算、退款和任务日志仍走原独立状态机，不使用这个普通 Provider 工具售价表。
这也不表示 Provider 文件、WebSocket、其他公开入口、所有厂商私有工具用量或完整 New API
宿主兼容已经完成。

离线回归使用 JS 子进程、模拟 HTTP、现有账本 stub 和独立组件测试，不调用付费厂商。
