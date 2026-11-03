const numberFormat = new Intl.NumberFormat('zh-CN')

const bucketTimeFormat = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Asia/Shanghai',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
})

const bucketDateFormat = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Asia/Shanghai',
  month: '2-digit',
  day: '2-digit',
})

const clockFormat = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Asia/Shanghai',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hourCycle: 'h23',
})

export function formatNumber(value: number | null | undefined): string {
  return numberFormat.format(value ?? 0)
}

/** Formats a 0..1 ratio as a percentage. */
export function formatPercent(
  ratio: number | null | undefined,
  digits = 1,
): string {
  return `${((ratio ?? 0) * 100).toFixed(digits)}%`
}

export function formatDuration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return '—'
  return `${numberFormat.format(Math.round(ms))} ms`
}

/** Formats a dashboard trend bucket in Asia/Shanghai. */
export function formatBucketLabel(
  iso: string,
  interval: 'hour' | 'day',
): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return interval === 'day'
    ? bucketDateFormat.format(date)
    : bucketTimeFormat.format(date)
}

/** Formats a clock time in Asia/Shanghai. */
export function formatClock(value: Date | string | null | undefined): string {
  if (!value) return ''
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return clockFormat.format(date)
}

export function quotaProgressStatus(
  ratio: number | null | undefined,
): 'warning' | 'exception' | undefined {
  const pct = ratio ?? 0
  if (pct >= 0.95) return 'exception'
  if (pct >= 0.8) return 'warning'
  return undefined
}
