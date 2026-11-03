import { describe, expect, it } from 'vitest'

import {
  formatBucketLabel,
  formatDuration,
  formatNumber,
  formatPercent,
  quotaProgressStatus,
} from './format'

describe('format utilities', () => {
  it('formats counts and percentages', () => {
    expect(formatNumber(12345)).toBe('12,345')
    expect(formatNumber(null)).toBe('0')
    expect(formatPercent(0.256)).toBe('25.6%')
    expect(formatPercent(1, 0)).toBe('100%')
  })

  it('formats latency and empty values', () => {
    expect(formatDuration(385.4)).toBe('385 ms')
    expect(formatDuration(null)).toBe('—')
  })

  it('formats trend buckets in Shanghai time', () => {
    expect(formatBucketLabel('2026-11-02T02:00:00Z', 'hour')).toBe('10:00')
    expect(formatBucketLabel('2026-11-02T16:00:00Z', 'day')).toBe('11-03')
  })

  it('maps quota pressure to progress status', () => {
    expect(quotaProgressStatus(0.4)).toBeUndefined()
    expect(quotaProgressStatus(0.85)).toBe('warning')
    expect(quotaProgressStatus(0.99)).toBe('exception')
  })
})
