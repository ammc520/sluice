import { describe, expect, it } from 'vitest'

import {
  safeAdminRedirect,
  safePortalRedirect,
  safeRedirect,
} from './redirect'

describe('safeRedirect', () => {
  it('accepts router-internal paths', () => {
    expect(safeRedirect('/admin/audit?page=2')).toBe('/admin/audit?page=2')
  })

  it('rejects external, protocol-relative, and backslash paths', () => {
    expect(safeRedirect('https://evil.example')).toBe('/admin/dashboard')
    expect(safeRedirect('//evil.example')).toBe('/admin/dashboard')
    expect(safeRedirect('/\\evil.example')).toBe('/admin/dashboard')
    expect(safeRedirect(undefined)).toBe('/admin/dashboard')
  })
})

describe('safeAdminRedirect', () => {
  it('keeps destination paths inside the admin console', () => {
    expect(safeAdminRedirect('/admin/employees?page=2')).toBe(
      '/admin/employees?page=2',
    )
  })

  it('never redirects outside /admin', () => {
    expect(safeAdminRedirect('/portal/overview')).toBe('/admin/dashboard')
    expect(safeAdminRedirect('https://evil.example')).toBe('/admin/dashboard')
    expect(safeAdminRedirect('//evil.example')).toBe('/admin/dashboard')
    expect(safeAdminRedirect(undefined)).toBe('/admin/dashboard')
  })

  it('avoids bouncing back to the login page or the bare admin root', () => {
    expect(safeAdminRedirect('/admin/login')).toBe('/admin/dashboard')
    expect(safeAdminRedirect('/admin/login?redirect=%2Fadmin')).toBe(
      '/admin/dashboard',
    )
    expect(safeAdminRedirect('/admin')).toBe('/admin/dashboard')
  })
})

describe('safePortalRedirect', () => {
  it('keeps authenticated destination paths inside the portal', () => {
    expect(safePortalRedirect('/portal/requests?status=approved')).toBe(
      '/portal/requests?status=approved',
    )
  })

  it('never redirects outside /portal', () => {
    expect(safePortalRedirect('/admin/dashboard')).toBe('/portal/overview')
    expect(safePortalRedirect('https://evil.example')).toBe('/portal/overview')
    expect(safePortalRedirect('//evil.example')).toBe('/portal/overview')
    expect(safePortalRedirect(undefined)).toBe('/portal/overview')
  })

  it('avoids bouncing back to the login page or the bare portal root', () => {
    expect(safePortalRedirect('/portal/login')).toBe('/portal/overview')
    expect(safePortalRedirect('/portal/login?redirect=%2Fportal')).toBe(
      '/portal/overview',
    )
    expect(safePortalRedirect('/portal')).toBe('/portal/overview')
  })
})
