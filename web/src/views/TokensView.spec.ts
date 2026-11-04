import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Employee, Token } from '../api/types'
import { useAuthStore } from '../stores/auth'
import TokensView from './TokensView.vue'

const alice: Employee = {
  id: 'employee-1',
  employee_no: 'E001',
  name: 'Alice',
  email: 'alice@example.com',
  department_id: 1,
  department_name: 'Engineering',
  status: 1,
  quota_type: 'tokens',
  quota_period: 'monthly',
  quota_total: 1_000_000,
  quota_used: 0,
  quota_period_start: '2026-11-01T00:00:00Z',
  created_at: '2026-11-02T00:00:00Z',
  updated_at: '2026-11-02T00:00:00Z',
}

const token: Token = {
  id: 'token-1',
  employee_id: 'employee-1',
  name: 'CI',
  token_prefix: 'sk-company-abcd',
  token_last4: 'wxyz',
  quota_type: 'tokens',
  quota_period: 'monthly',
  quota_total: 100000,
  quota_used: 1200,
  period_start: '2026-11-01T00:00:00Z',
  rate_limit_rpm: 60,
  ip_allowlist: [],
  status: 1,
  expires_at: '2026-12-01T00:00:00Z',
  created_at: '2026-11-02T00:00:00Z',
  last_used_at: null,
}

function response(data: unknown): Response {
  return {
    ok: true,
    status: 200,
    json: vi.fn().mockResolvedValue({ data, error: null }),
  } as unknown as Response
}

function page(items: unknown[]) {
  return { items, total: items.length, page: 1, page_size: 20 }
}

function mountView(role: 'super_admin' | 'operator' | 'auditor' = 'super_admin') {
  const pinia = createPinia()
  setActivePinia(pinia)
  const auth = useAuthStore(pinia)
  auth.user = {
    id: 'admin-1',
    username: 'admin',
    role,
    status: 1,
    created_at: '2026-11-02T00:00:00Z',
  }
  auth.initialized = true
  return mount(TokensView, {
    global: {
      plugins: [pinia, ElementPlus],
      stubs: { teleport: true },
    },
  })
}

describe('TokensView', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes('/employees?')) return Promise.resolve(response(page([alice])))
      return Promise.resolve(response(page([token])))
    })
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders token usage, employee identity, and expiry', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('Alice（E001）')
    expect(wrapper.text()).toContain('sk-company-abcd…wxyz')
    expect(wrapper.text()).toContain('每月 · Token 数')
    expect(wrapper.text()).toContain('1,200 / 100,000')
    expect(wrapper.text()).toContain('2026-12-01 08:00')
    expect(wrapper.text()).toContain('正常')
  })

  it('hides token mutations from auditors', async () => {
    const wrapper = mountView('auditor')
    await flushPromises()

    expect(wrapper.find('[data-test="create-token"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('轮换')
  })
})
