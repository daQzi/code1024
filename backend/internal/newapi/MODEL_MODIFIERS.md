# 模型参数后缀

固定基线：New API `48f9df96ea7c68eae04a0f3687869f232f48929f`。

当前已接入普通 Provider（HTTP Chat Completions / Responses / Anthropic Messages，含受校验 RelayKit 预置与 `+plugin` 组合）以及原生 HTTP 文本入口（OpenAI Chat Completions / Responses / Messages 兼容、Anthropic Messages 及 Chat/Responses 兼容、Gemini `/v1beta/models/*` 与 Messages 兼容、Antigravity Messages/Gemini）。Responses WebSocket 执行接点仍待后续批次完成。

示例：`public-model@effort:high@temperature:0.2@topp:0.8`。语法是冒号，不是等号。仅解析名称末尾连续的 `@key:value`，`vendor/model@revision` 保留原义。

- 支持 `thinking`、`effort`、`temperature`、`topp`；使用相同 RelayKit parser 和 reasoning intent。thinking 可为开关、adaptive 或整数 budget；非法值、未知参数、NaN/Inf 在发送前拒绝。
- 同参数最右边的值优先；账号映射目标中的参数按维度覆盖客户端参数。公开模型名保留，实际上游 URL 和 JSON 使用 base 模型。
- 分组白名单、账号模型映射和 Transform 实例/源码模型范围同时匹配完整名称和 base；模型价格先找原名，再找标准 thinking/effort 名，最后找 base，不补造单价。
- 精确 thinking budget 通过私有 DTO 状态传给共享 codec，不作为自定义 JSON 字段发给厂商。`reasoning.set` 默认尊重已有后缀，显式 `override:true` 可覆盖；晚 ingress/outbound 或原样 Provider prepare 修改实际控制后，清除旧私有状态并按新实际控制记录计费身份。
- 纯 `none` / `plugin` 路由仍由其实际 JSON/脚本决定厂商能力；私有 DTO 状态只供宿主预置 codec 使用。特别是 Responses 没有通用精确 budget 字段，不声称任意插件都能读取该私有状态。
- 请求仍受 1 MiB、8192 节点、64 层限制；模型名称最多 4096 字节，最多 32 个修饰段。有控制参数时拒绝重复 JSON 键及控制字段大小写歧义。

真实模型名本身包含类似后缀时，可以在管理员“请求处理 → 模型名参数豁免”填写完整名称或 `re:` 表达式。默认沿用以下启动配置：

```yaml
gateway:
  model_modifier_exemptions:
    - "real-model@temperature:0.2"
    - "re:^vendor/.+@sha256:"
```

最多 128 项、每项 512 个 UTF-8 字节，`re:` 使用 Go RE2。此配置参考固定基线 `setting/model_setting/global.go` 的完整名称/正则豁免语义；白名单、路由、定价和 scope 共享同一原子替换的不可变策略，不建立请求或账号专属全局策略。

后台保存需管理员身份和既有二次验证。页面按需加载，保存携带读取时 revision；数据库条件更新使同时编辑最多一人成功，冲突保留草稿并要求手动刷新。显式空列表表示不豁免任何名称；“沿用启动配置”与空列表不同，恢复后按各节点当前启动配置生效，因此各节点启动配置应一致。

当前节点在数据库保存并重新读取确认后立即应用，其他节点在后续网关请求中最多缓存两秒。启动及过期刷新遇到无效配置或数据库故障时拒绝新准入，不偷偷回退为无豁免策略。共享读取有两秒截止时间；单个调用者取消不取消其他等待者，旧读取不能覆盖已保存的新配置。已接受任务和已构造 Provider 请求仍使用原快照。

离线回归覆盖映射覆盖、Chat/Responses JSON/SSE、非法后缀零上游调用、分组与 scope、价格回退、UTF/JSON 边界、精确 budget、显式请求插件覆盖、热更新/并发/过期刷新与管理权限；不代表真实渠道已完成联调。
