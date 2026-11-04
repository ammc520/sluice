import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { AdminUser } from '../api/types'
import { pinia } from '../stores'
import { useAuthStore } from '../stores/auth'
import { usePortalAuthStore } from '../stores/portalAuth'
import router from './index'

const adminUser: AdminUser = {
  id: 'admin-1',
  username: 'admin',
  role: 'super_admin',
  status: 1,
  created_at: '2026-11-02T00:00:00Z',
}

function response(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: vi.fn().mockResolvedValue(body),
  } as unknown as Response
}

function portalMe(mustChangePassword: boolean): Response {
  return response(200, {
    data: {
      employee: {
        id: 'employee-1',
        employee_no: 'E001',
        name: 'Alice',
        email: 'alice@example.com',
        department_id: 1,
        status: 1,
      },
      must_change_password: mustChangePassword,
    },
    error: null,
  })
}

async function visit(path: string) {
  await router.push(path)
  return router.currentRoute.value
}

describe('portal route guard', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    usePortalAuthStore(pinia).$reset()
  })

  it('redirects unauthenticated visitors to the portal login', async () => {
    fetchMock.mockResolvedValue(
      response(401, {
        data: null,
        error: { code: 'unauthorized', message: 'authentication required' },
      }),
    )

    const route = await visit('/portal/overview?tab=usage')

    expect(route.path).toBe('/portal/login')
    expect(route.query.redirect).toBe('/portal/overview?tab=usage')
  })

  it('treats a disabled portal (404) as unauthenticated', async () => {
    fetchMock.mockResolvedValue(
      response(404, {
        data: null,
        error: { code: 'not_found', message: 'not found' },
      }),
    )

    const route = await visit('/portal/tokens')

    expect(route.path).toBe('/portal/login')
  })

  it('forces a password change before any portal data page', async () => {
    fetchMock.mockResolvedValue(portalMe(true))

    const blocked = await visit('/portal/usage')
    expect(blocked.path).toBe('/portal/password')

    const allowed = await visit('/portal/password')
    expect(allowed.path).toBe('/portal/password')
  })

  it('allows an authenticated employee into the portal', async () => {
    fetchMock.mockResolvedValue(portalMe(false))

    const route = await visit('/portal/requests')

    expect(route.path).toBe('/portal/requests')
  })

  it('sends an authenticated employee away from the login page', async () => {
    fetchMock.mockResolvedValue(portalMe(false))

    const route = await visit('/portal/login')

    expect(route.path).toBe('/portal/overview')
  })

  it('sends an employee who must change password to the password page', async () => {
    fetchMock.mockResolvedValue(portalMe(true))

    const route = await visit('/portal/login')

    expect(route.path).toBe('/portal/password')
  })
})

describe('admin route guard', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    useAuthStore(pinia).$reset()
    usePortalAuthStore(pinia).$reset()
  })

  it('redirects unauthenticated administrators to /admin/login', async () => {
    fetchMock.mockResolvedValue(
      response(401, {
        data: null,
        error: { code: 'unauthorized', message: 'authentication required' },
      }),
    )

    const route = await visit('/admin/dashboard?range=7d')

    expect(route.path).toBe('/admin/login')
    expect(route.query.redirect).toBe('/admin/dashboard?range=7d')
  })

  it('no longer serves admin pages from the site root', async () => {
    fetchMock.mockResolvedValue(
      response(401, {
        data: null,
        error: { code: 'unauthorized', message: 'authentication required' },
      }),
    )

    const route = await visit('/tokens?employee_id=e-1')

    expect(route.path).toBe('/portal/login')
  })

  it('sends an authenticated administrator from login to the dashboard', async () => {
    fetchMock.mockResolvedValue(
      response(200, { data: { user: adminUser }, error: null }),
    )

    const route = await visit('/admin/login')

    expect(route.path).toBe('/admin/dashboard')
  })
})
