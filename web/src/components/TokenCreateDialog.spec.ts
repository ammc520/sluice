import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { describe, expect, it } from 'vitest'

import type { Employee } from '../api/types'
import TokenCreateDialog from './TokenCreateDialog.vue'

const employee: Employee = {
  id: 'employee-1',
  employee_no: 'E001',
  name: 'Alice',
  email: 'alice@example.com',
  department_id: 1,
  status: 1,
  created_at: '2026-11-02T00:00:00Z',
  updated_at: '2026-11-02T00:00:00Z',
}

function mountDialog(props: Record<string, unknown>) {
  return mount(TokenCreateDialog, {
    props: { modelValue: true, ...props },
    global: {
      plugins: [ElementPlus],
      stubs: { teleport: true },
    },
  })
}

describe('TokenCreateDialog', () => {
  it('shows the full token once and closes after confirmation', async () => {
    const wrapper = mountDialog({ token: 'sk-company-abc' })
    await flushPromises()

    expect(wrapper.text()).toContain('sk-company-abc')
    expect(wrapper.text()).toContain('完整 Token 仅显示一次')

    await wrapper.find('[data-test="confirm-saved"]').trigger('click')

    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([false])
  })

  it('emits the normalized token settings from the create form', async () => {
    const wrapper = mountDialog({ employees: [employee] })
    await flushPromises()

    const textarea = wrapper.find('textarea')
    await textarea.setValue('10.0.0.0/8\n\n 192.168.1.1 ')
    await wrapper.find('[data-test="save-token"]').trigger('click')

    const payload = wrapper.emitted('submit')?.[0]?.[0] as Record<string, unknown>
    expect(payload).toMatchObject({
      employee_id: 'employee-1',
      quota_type: 'tokens',
      quota_period: 'monthly',
      quota_total: null,
      rate_limit_rpm: 60,
      expires_at: null,
      ip_allowlist: ['10.0.0.0/8', '192.168.1.1'],
    })
  })

  it('does not submit when no employee is selected', async () => {
    const wrapper = mountDialog({ employees: [] })
    await flushPromises()

    await wrapper.find('[data-test="save-token"]').trigger('click')

    expect(wrapper.emitted('submit')).toBeUndefined()
    expect(wrapper.find('[data-test="token-form-error"]').text()).toContain(
      '请选择员工',
    )
  })
})
