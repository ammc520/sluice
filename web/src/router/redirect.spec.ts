import { describe, expect, it } from 'vitest'

import { safeRedirect } from './redirect'

describe('safeRedirect', () => {
  it('accepts router-internal paths', () => {
    expect(safeRedirect('/audit?page=2')).toBe('/audit?page=2')
  })

  it('rejects external, protocol-relative, and backslash paths', () => {
    expect(safeRedirect('https://evil.example')).toBe('/dashboard')
    expect(safeRedirect('//evil.example')).toBe('/dashboard')
    expect(safeRedirect('/\\evil.example')).toBe('/dashboard')
    expect(safeRedirect(undefined)).toBe('/dashboard')
  })
})
