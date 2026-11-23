export default {
  taskLogs: {
    maintenance: { hint: '请先核查原上游任务和连接。恢复仅查询原任务或按原价格、原付款来源重试结算；操作会记录审核依据。', action: '处理方式', resume_poll: '恢复后台查询', retry_settlement: '重试原账本结算', reason: '核查依据（至少 4 个字符，请勿填写密钥）', confirm: '确认恢复' },
    billingIssue: '结算待处理原因',
    title: '任务日志', description: '查看生成任务的进度、结果与结算状态',
    tabs: { all: '全部任务', mine: '我的任务', review: '资金对账' },
    reviewOnly: '仅待处理任务',
    autoRefresh: '自动刷新', taskId: '任务 ID', model: '模型', plugin: '插件', billing: '计费状态',
    start: '开始时间', end: '结束时间', created: '提交时间', group: '分组', userAccount: '用户 / 上游账号',
    duration: '耗时', details: '任务详情', empty: '暂无任务', waitingProgress: '等待上游进度',
    protocol: '协议 / 动作', held: '冻结金额', settled: '结算金额', upstreamId: '上游任务 ID',
    prompt: '提示词', parameters: '时长 / 尺寸', failure: '失败原因', timeline: '任务时间线',
    download: '下载制品', downloadHint: '通过本站鉴权下载生成的视频', loadFailed: '任务加载失败，请重试',
    invalidRange: '结束时间必须晚于开始时间',
    coverage: '展示插件任务和本站记录过创建的 Grok / 火山原生视频任务。标有“后台跟进”的任务会自动查询进度，其余随原接口查询或下载更新。结束时间为本站首次观察到终态的时间；未返回百分比时只显示任务阶段。',
    nativeSource: '原生协议',
    taskIdHint: '日志 ID 或原生接口返回的任务 ID',
    nativeHint: '原生任务沿用原接口查询、下载与计费。显示“后台跟进”的任务会自动查询进度；更早的任务及 OAuth 任务仍需通过原接口查询。保存了创建时计费规则的新普通 API Key 任务会在取得实际用量后自动结算，其余继续原计费流程。未确认原账本成功结算时金额留空，不表示免费。',
    monitoring: '后台跟进',
    monitorStatuses: { active: '后台跟进中', complete: '后台跟进结束', review: '后台跟进待处理' },
    monitorErrors: { timeout: '超过自动跟进期限，请核查上游任务', account_unavailable: '原账号不可用，请检查账号状态', connection_changed: '原连接配置已改变，请恢复原配置后核查任务', auth_failed: '上游认证未通过，请检查原账号密钥', capacity: '账号查询容量暂不可用', upstream_error: '上游查询连续失败，请通过原接口核查', response_limit: '上游查询响应过大，请核查协议', unrecognized: '上游状态无法识别，请核查协议和任务号', usage_pending: '任务已完成，但上游尚未提供有效用量，等待核查' },
    statuses: { queued: '排队中', submitting: '提交中', submission_uncertain: '提交结果待确认', running: '生成中', succeeded: '生成成功', failed: '失败' },
    billingStatuses: { held: '已冻结', pending: '待结算', settled: '已结算', review: '待对账', unconfirmed: '原账本待确认', not_charged: '未扣费' },
    deliveryStatuses: { pending: '待交付', ready: '制品可用', failed: '交付失败' },
    events: { native_resume_poll: '管理员恢复后台查询', native_retry_settlement: '管理员重试原账本结算', funds_held: '创建任务并冻结费用', funds_settled: '完成资金结算', administrator_reconciliation: '管理员处理对账', updated: '任务状态更新', native_submitted: '原生接口接受任务', native_running: '观察到任务生成中', native_succeeded: '首次观察到任务完成', native_failed: '首次观察到任务失败或过期', native_monitor_review: '后台跟进暂停，等待核查', native_billing_settled: '原账本自动结算完成', native_billing_logged: '已记录用量，未扣除资金', native_billing_ignored: '任务失败，未扣费', native_billing_review: '自动结算暂停，等待核查' }
  }
}
