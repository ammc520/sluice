import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Employee } from '../api/types'
import { useAuthStore } from '../stores/auth'
import EmployeesView from './EmployeesView.vue'

const alice: Employee = {
  id: 'employee-1',
  employee_no: 'E001',
  name: 'Alice',
  email: 'alice@example.com',
  department_id: 1,
  status: 1,
  created_at: '2026-11-02T00:00:00Z',
  updated_at: '2026-11-02T00:00:00Z',
}

function response(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    json: vi.fn().mockResolvedValue(body),
  } as unknown as Response
}

function page(items: Employee[]) {
  return {
    data: {
      items,
      total: items.length,
      page: 1,
      page_size: 20,
    },
    error: null,
  }
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
  return mount(EmployeesView, {
    global: {
      plugins: [pinia, ElementPlus],
      stubs: { teleport: true },
    },
  })
}

describe('EmployeesView', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn().mockResolvedValue(response(page([alice])))
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('loads and renders employees in the Shanghai timezone', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('Alice')
    expect(wrapper.text()).toContain('E001')
    expect(wrapper.text()).toContain('启用')
    expect(wrapper.text()).toContain('2026-11-02 08:00')
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      '/admin/api/employees?page=1&page_size=20',
    )
  })

  it('sends the search term when the query button is clicked', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="employee-search"]').setValue('alice')
    await wrapper.find('[data-test="employee-search-button"]').trigger('click')
    await flushPromises()

    const urls = fetchMock.mock.calls.map((call) => String(call[0]))
    expect(urls[urls.length - 1]).toContain('q=alice')
  })

  it('hides employee management actions from auditors', async () => {
    const wrapper = mountView('auditor')
    await flushPromises()

    expect(wrapper.find('[data-test="create-employee"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('编辑')
  })

  it('creates an employee and refreshes the list', async () => {
    fetchMock.mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.method === 'POST') {
        return Promise.resolve(response({ data: alice, error: null }))
      }
      return Promise.resolve(response(page([alice])))
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="create-employee"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="employee-no"]').setValue('E002')
    await wrapper.find('[data-test="employee-name"]').setValue('Bob')
    await wrapper.find('[data-test="save-employee"]').trigger('click')
    await flushPromises()

    const postCall = fetchMock.mock.calls.find(
      (call) => (call[1] as RequestInit | undefined)?.method === 'POST',
    )
    expect(postCall).toBeDefined()
    expect(postCall?.[0]).toBe('/admin/api/employees')
    expect(JSON.parse(String((postCall?.[1] as RequestInit).body))).toMatchObject({
      employee_no: 'E002',
      name: 'Bob',
      status: 1,
    })
  })
})
