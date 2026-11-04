import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Department, Employee } from '../api/types'
import { useAuthStore } from '../stores/auth'
import EmployeesView from './EmployeesView.vue'

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
  quota_used: 1200,
  quota_period_start: '2026-11-01T00:00:00Z',
  created_at: '2026-11-02T00:00:00Z',
  updated_at: '2026-11-02T00:00:00Z',
}

const bob: Employee = {
  id: 'employee-2',
  employee_no: 'E002',
  name: 'Bob',
  email: 'bob@example.com',
  department_id: null,
  department_name: '',
  status: 1,
  quota_type: null,
  quota_period: null,
  quota_total: null,
  quota_used: 0,
  quota_period_start: null,
  created_at: '2026-11-03T00:00:00Z',
  updated_at: '2026-11-03T00:00:00Z',
}

const engineering: Department = {
  id: 1,
  name: 'Engineering',
  employee_count: 1,
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

function directory(
  items: Department[] = [engineering],
  unassigned = 1,
  total = 2,
) {
  return {
    data: {
      items,
      unassigned_count: unassigned,
      total_employees: total,
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

function lastURL(fetchMock: ReturnType<typeof vi.fn>): string {
  const calls = fetchMock.mock.calls
  return String(calls[calls.length - 1]?.[0])
}

describe('EmployeesView', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn().mockImplementation((url: string) => {
      if (String(url).includes('/departments')) {
        return Promise.resolve(response(directory()))
      }
      return Promise.resolve(response(page([alice, bob])))
    })
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('loads departments and employees in the Shanghai timezone', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('团队管理')
    expect(wrapper.text()).toContain('Alice')
    expect(wrapper.text()).toContain('E001')
    expect(wrapper.text()).toContain('Engineering')
    expect(wrapper.text()).toContain('未分配')
    expect(wrapper.text()).toContain('2026-11-02 08:00')
    expect(
      fetchMock.mock.calls.some((call) =>
        String(call[0]).startsWith('/admin/api/departments'),
      ),
    ).toBe(true)
    expect(
      fetchMock.mock.calls.some((call) =>
        String(call[0]).startsWith('/admin/api/employees?page=1'),
      ),
    ).toBe(true)
  })

  it('filters employees by department and unassigned bucket', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="department-1"]').trigger('click')
    await flushPromises()
    expect(lastURL(fetchMock)).toContain('department_id=1')

    await wrapper.find('[data-test="department-unassigned"]').trigger('click')
    await flushPromises()
    expect(lastURL(fetchMock)).toContain('unassigned=1')

    await wrapper.find('[data-test="department-all"]').trigger('click')
    await flushPromises()
    expect(lastURL(fetchMock)).not.toContain('department_id')
    expect(lastURL(fetchMock)).not.toContain('unassigned')
  })

  it('sends the search term and status filter when querying', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="employee-search"]').setValue('alice')
    await wrapper.find('[data-test="employee-search-button"]').trigger('click')
    await flushPromises()

    expect(lastURL(fetchMock)).toContain('q=alice')
  })

  it('hides management actions from auditors', async () => {
    const wrapper = mountView('auditor')
    await flushPromises()

    expect(wrapper.find('[data-test="create-employee"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="create-department"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="rename-department-1"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="delete-department-1"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('编辑')
  })

  it('creates an employee and defaults to the selected department', async () => {
    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === 'POST' && String(url).includes('/employees')) {
        return Promise.resolve(response({ data: alice, error: null }))
      }
      if (String(url).includes('/departments')) {
        return Promise.resolve(response(directory()))
      }
      return Promise.resolve(response(page([alice, bob])))
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="department-1"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="create-employee"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="employee-no"]').setValue('E002')
    await wrapper.find('[data-test="employee-name"]').setValue('Bob')
    await wrapper.find('[data-test="save-employee"]').trigger('click')
    await flushPromises()

    const postCall = fetchMock.mock.calls.find(
      (call) =>
        String(call[0]).includes('/employees') &&
        (call[1] as RequestInit | undefined)?.method === 'POST',
    )
    expect(postCall).toBeDefined()
    expect(JSON.parse(String((postCall?.[1] as RequestInit).body))).toMatchObject({
      employee_no: 'E002',
      name: 'Bob',
      department_id: 1,
      status: 1,
    })
  })

  it('creates a department from the side panel', async () => {
    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === 'POST' && String(url).endsWith('/departments')) {
        return Promise.resolve(
          response({
            data: { id: 2, name: 'Sales', employee_count: 0 },
            error: null,
          }),
        )
      }
      if (String(url).includes('/departments')) {
        return Promise.resolve(response(directory()))
      }
      return Promise.resolve(response(page([alice, bob])))
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="create-department"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="department-name"]').setValue('Sales')
    await wrapper.find('[data-test="save-department"]').trigger('click')
    await flushPromises()

    const postCall = fetchMock.mock.calls.find(
      (call) =>
        String(call[0]).endsWith('/departments') &&
        (call[1] as RequestInit | undefined)?.method === 'POST',
    )
    expect(postCall).toBeDefined()
    expect(JSON.parse(String((postCall?.[1] as RequestInit).body))).toEqual({
      name: 'Sales',
    })
  })

  it('renames a department', async () => {
    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === 'PUT' && String(url).includes('/departments/1')) {
        return Promise.resolve(
          response({
            data: { id: 1, name: 'Platform', employee_count: 1 },
            error: null,
          }),
        )
      }
      if (String(url).includes('/departments')) {
        return Promise.resolve(response(directory()))
      }
      return Promise.resolve(response(page([alice, bob])))
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="rename-department-1"]').trigger('click')
    await flushPromises()
    const input = wrapper.find('[data-test="department-name"]')
    expect((input.element as HTMLInputElement).value).toBe('Engineering')
    await input.setValue('Platform')
    await wrapper.find('[data-test="save-department"]').trigger('click')
    await flushPromises()

    const putCall = fetchMock.mock.calls.find(
      (call) => (call[1] as RequestInit | undefined)?.method === 'PUT',
    )
    expect(putCall).toBeDefined()
    expect(String(putCall?.[0])).toBe('/admin/api/departments/1')
    expect(JSON.parse(String((putCall?.[1] as RequestInit).body))).toEqual({
      name: 'Platform',
    })
  })

  it('moves employees before deleting a department', async () => {
    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (
        init?.method === 'DELETE' &&
        String(url).includes('/departments/1')
      ) {
        return Promise.resolve(
          response({
            data: { deleted: true, moved_employees: 1 },
            error: null,
          }),
        )
      }
      if (String(url).includes('/departments')) {
        return Promise.resolve(response(directory()))
      }
      return Promise.resolve(response(page([alice, bob])))
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="delete-department-1"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="confirm-delete-department"]').trigger('click')
    await flushPromises()

    const deleteCall = fetchMock.mock.calls.find(
      (call) => (call[1] as RequestInit | undefined)?.method === 'DELETE',
    )
    expect(deleteCall).toBeDefined()
    expect(String(deleteCall?.[0])).toBe(
      '/admin/api/departments/1?move_to=none',
    )
  })
})
