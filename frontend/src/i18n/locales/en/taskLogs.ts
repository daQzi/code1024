export default {
  taskLogs: {
    maintenance: { hint: 'Verify the original upstream task and connection first. Resume queries the existing task or retries settlement with its captured price and payer. The review reason is recorded.', action: 'Action', resume_poll: 'Resume background queries', retry_settlement: 'Retry original ledger settlement', reason: 'Review reason (at least 4 characters; omit credentials)', confirm: 'Confirm resume' },
    billingIssue: 'Settlement review reason',
    title: 'Task Logs', description: 'Track generation progress, results and settlement',
    tabs: { all: 'All tasks', mine: 'My tasks', review: 'Reconciliation' },
    reviewOnly: 'Only tasks needing attention',
    autoRefresh: 'Auto refresh', taskId: 'Task ID', model: 'Model', plugin: 'Plugin', billing: 'Billing status',
    start: 'Start time', end: 'End time', created: 'Submitted', group: 'Group', userAccount: 'User / Upstream account',
    duration: 'Duration', details: 'Task details', empty: 'No tasks found', waitingProgress: 'Awaiting upstream progress',
    protocol: 'Protocol / Action', held: 'Reserved amount', settled: 'Settled amount', upstreamId: 'Upstream task ID',
    prompt: 'Prompt', parameters: 'Duration / Size', failure: 'Failure reason', timeline: 'Task timeline',
    download: 'Download artifact', downloadHint: 'Download the generated video through authenticated access', loadFailed: 'Unable to load tasks. Please retry.',
    invalidRange: 'End time must be after start time',
    coverage: 'Shows plugin tasks and native Grok / Ark videos whose creation was recorded by this gateway. Monitored tasks update automatically; others update on original endpoint queries or downloads. Completion time is the first observed terminal state. Stages are shown when no percentage is reported.',
    nativeSource: 'Native protocol',
    taskIdHint: 'Log ID or task ID returned by the native endpoint',
    nativeHint: 'Native tasks keep their original query, download and billing endpoints. Tasks marked as monitored are polled automatically; older and OAuth tasks still require their original query endpoints. New ordinary API key tasks with a captured price settle automatically once actual usage is available; other tasks keep their original billing flow. An unconfirmed amount is blank and does not mean the task is free.',
    monitoring: 'Background monitoring',
    monitorStatuses: { active: 'Monitoring', complete: 'Monitoring complete', review: 'Monitoring needs attention' },
    monitorErrors: { timeout: 'Automatic monitoring expired; check the upstream task', account_unavailable: 'The original account is unavailable; check its status', connection_changed: 'The original connection changed; restore it and check the task', auth_failed: 'Upstream authentication failed; check the original account key', capacity: 'Account query capacity is temporarily unavailable', upstream_error: 'Repeated status queries failed; check the original endpoint', response_limit: 'The upstream status response is too large; check the protocol', unrecognized: 'The upstream status is unrecognized; check the protocol and task ID', usage_pending: 'Generation finished but valid usage is still missing; check the upstream task' },
    statuses: { queued: 'Queued', submitting: 'Submitting', submission_uncertain: 'Submission needs confirmation', running: 'Generating', succeeded: 'Generated', failed: 'Failed' },
    billingStatuses: { held: 'Reserved', pending: 'Pending settlement', settled: 'Settled', review: 'Needs reconciliation', unconfirmed: 'Native ledger unconfirmed', not_charged: 'Not charged' },
    deliveryStatuses: { pending: 'Awaiting delivery', ready: 'Artifact ready', failed: 'Delivery failed' },
    events: { native_resume_poll: 'Administrator resumed queries', native_retry_settlement: 'Administrator retried native settlement', funds_held: 'Task created and funds reserved', funds_settled: 'Funds settled', administrator_reconciliation: 'Administrator reconciliation', updated: 'Task status updated', native_submitted: 'Native endpoint accepted the task', native_running: 'Observed generation in progress', native_succeeded: 'First observed completion', native_failed: 'First observed failure or expiration', native_monitor_review: 'Monitoring paused for review', native_billing_settled: 'Native ledger settled automatically', native_billing_logged: 'Usage recorded without charging funds', native_billing_ignored: 'Task failed without a charge', native_billing_review: 'Automatic settlement paused for review' }
  }
}
