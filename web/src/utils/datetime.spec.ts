import { describe, expect, it } from 'vitest'

import { formatDateTime, toShanghaiInput } from './datetime'

describe('Shanghai datetime formatting', () => {
  it('renders UTC timestamps in Asia/Shanghai', () => {
    expect(formatDateTime('2026-11-02T16:00:00Z')).toBe('2026-11-03 00:00')
    expect(toShanghaiInput('2026-11-02T16:00:00Z')).toBe('2026-11-03T00:00:00')
  })

  it('returns a placeholder for missing or invalid values', () => {
    expect(formatDateTime(null)).toBe('—')
    expect(formatDateTime('not-a-time')).toBe('—')
    expect(toShanghaiInput(undefined)).toBe('')
  })
})
