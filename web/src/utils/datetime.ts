const shanghaiFormatter = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hourCycle: 'h23',
})

interface ShanghaiParts {
  date: string
  time: string
}

function shanghaiParts(value: string | null | undefined): ShanghaiParts | null {
  if (!value) return null
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return null

  const parts = shanghaiFormatter.formatToParts(date)
  const read = (type: Intl.DateTimeFormatPartTypes): string =>
    parts.find((part) => part.type === type)?.value ?? ''

  return {
    date: `${read('year')}-${read('month')}-${read('day')}`,
    time: `${read('hour')}:${read('minute')}:${read('second')}`,
  }
}

/** Formats an API timestamp in Asia/Shanghai for display. */
export function formatDateTime(value: string | null | undefined): string {
  const parts = shanghaiParts(value)
  return parts ? `${parts.date} ${parts.time.slice(0, 5)}` : '—'
}

/** Formats an API timestamp for an Element Plus datetime input, whose value
 * format the backend interprets as Asia/Shanghai wall-clock time. */
export function toShanghaiInput(value: string | null | undefined): string {
  const parts = shanghaiParts(value)
  return parts ? `${parts.date}T${parts.time}` : ''
}
