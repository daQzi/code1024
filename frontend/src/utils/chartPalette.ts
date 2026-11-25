/**
 * 图表序列调色板。
 *
 * 全站配色是「墨韵」：单一墨玉主色 + 中性灰 + 古铜点缀。
 * 图表不能再用 12 色彩虹调色板（#3b82f6 / #10b981 / #f59e0b … ），
 * 那套颜色会让饼图、堆叠图变成「数据可视化模板」，
 * 与界面的低饱和基调互相打架。
 *
 * 这里用同一色系的深浅区分序列：靠序号位置辨识，而不是靠色相。
 * 需要更多序列时会循环使用，仍保持整体色相统一。
 */
export const CHART_SERIES_COLORS = [
  '#3f635a',
  '#5d8b7f',
  '#7ba79b',
  '#4a7268',
  '#9c7742',
  '#b4915f',
  '#35564e',
  '#a3c3ba',
  '#7d5c31',
  '#2a473f',
  '#c9b48c',
  '#213832',
] as const

/** 取前 n 个序列色；超出调色板长度时循环补齐。 */
export function chartSeriesColors(count: number): string[] {
  if (count <= 0) return []
  return Array.from(
    { length: count },
    (_, index) => CHART_SERIES_COLORS[index % CHART_SERIES_COLORS.length],
  )
}

/** 「其它」聚合项等需要刻意弱化的序列，用中性灰而不是强调色。 */
export const CHART_MUTED_COLOR = '#a9aeaa'
