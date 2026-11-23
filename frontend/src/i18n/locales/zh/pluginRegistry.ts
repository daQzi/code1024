export default {
  pluginRegistry: {
    external: {
      code: '插件编号', codeHint: '输入 8 位插件编号', lookup: '按编号查找', clearLookup: '返回市场列表', lookupSource: '查找结果来自', notFound: '已配置来源中未找到该插件编号',
      compareInstalled: '与本站已安装版本对比', currentInstalled: '当前已安装版本（默认）',
      title: '外部插件市场', hint: '读取 New API API v1 市场索引，按版本查看源码、说明和变更记录。浏览器需要能访问来源并获得跨域许可；连接失败时可切换镜像或从本地上传。',
      manageSources: '管理市场来源', source: '市场来源', sourceName: '来源名称', indexURL: '索引地址', version: '安装版本', search: '搜索名称、模型、插件 key 或编号', origins: { official: 'New API 官方源', community: 'New API 社区源', third_party: '第三方来源' },
      sourcesHint: '最多 16 个来源，配置对本站管理员共用。使用无凭据、查询参数和片段的 HTTP(S) 索引地址。自定义来源始终标为第三方，保存需要管理员二次验证。',
      hosts: '索引声明的允许主机', baseURL: '索引声明的默认地址', recommended: '来源推荐', notInstalled: '尚未安装', current: '当前版本与最新摘要一致', installedVersion: '已安装 v{version}，可查看其他版本', readme: '使用说明', changelog: '版本变更',
      compatibilityHint: '索引内容仅供展示。安装前重新读取索引、检查撤回记录并核对 SHA256；服务端再次核对实际源码、名称和版本。安装不会自动启用。能否运行以安装后的宿主兼容检查为准。插件可以读取绑定账号的凭据，请审阅源码。',
      errors: { code: '插件编号必须是 8 位数字', url: '请输入有效的无凭据、查询参数和片段的 HTTP(S) 地址', path: '插件文件地址必须与索引同源，且不能重定向', index: '索引格式无效、重复或超过限制；仅支持 indexVersion 1', empty: '来源未返回可读取的正文', large: '内容超过大小限制（索引 2 MiB、源码 1 MiB、文档 64 KiB）', timeout: '读取市场来源超时，请切换来源或稍后重试', hashMissing: '该版本缺少有效 SHA256，不能从市场安装', hashMismatch: '下载的内容与索引 SHA256 不符，已阻止安装', changed: '该版本或摘要已从索引改变，请刷新后重新查看', revoked: '此摘要已被来源撤回，不能安装', apiVersion: '此版本需要更高版本的插件 API，当前宿主不能安装' }
    },
    playground: { title: '离线试运行', template: '新插件模板', kinds: { ingress: '入口处理（Ingress）', task: '视频任务（Task）', provider: '文本渠道（Provider）' }, hint: '仅运行当前源码和你填写的模拟数据。请勿填写真实密钥或用户数据；不会联网、生成视频、保存任务或扣费。参数总计最多 64 KiB，执行最多 5 秒。', fixture: '模拟输入（hook 和 args 的 JSON）', run: '运行当前源码', invalidFixture: '请填写包含 hook 字符串和 args 数组的 JSON', templateUnavailable: '模板尚未加载，请刷新后重试', resultHint: '这是单个函数的输出检查，不能代替完整协议、文件、账单与真实上游验收。' },
    title: '插件开发', titles: { mine: '我的插件', review: '社区投稿审核', market: '社区插件市场' },
    hints: { mine: '上传源码并校验为草稿，提交后由管理员审核。源码不应包含账号密码或 API Key。', review: '检查源码、说明和宿主兼容情况后发布。发布只让插件进入市场，安装和启用分别操作。', market: '本站审核发布的社区版本。查看完整源码和 SHA256 后按固定内容安装。' },
    create: '新建插件', plugin: '插件 / 版本', author: '发布者', updated: '更新时间', open: '查看源码', empty: '暂无插件版本', editor: '插件源码与发布',
    namespaceHint: '你的插件 key 前缀为 {namespace}，总长不超过 30 个字符。沿用 New API plugin API v1 契约，宿主兼容提示决定当前能否实际启用。',
    revision: '修订', reviewReason: '审核或撤回原因', readme: '使用说明与变更记录', upload: '读取本地 plugin.js',
    validateSave: '校验并保存草稿', submit: '提交审核', publish: '审核并发布', reject: '退回修改', withdraw: '撤回版本', install: '按此 SHA256 安装',
    tooLarge: '源码不能超过 1 MiB', saved: '源码已通过校验并保存，尚未安装或启用。', installed: '此版本已安装，启停状态保持不变，可到已安装列表管理。',
    immutableHint: '提交后源码锁定；已发布版本不能修改，请增加版本号创建新投稿。撤回后停止市场安装，已有安装版本和在途任务继续保留。',
    statuses: { draft: '草稿', submitted: '待审核', rejected: '已退回', published: '已发布', withdrawn: '已撤回' }
  }
}
