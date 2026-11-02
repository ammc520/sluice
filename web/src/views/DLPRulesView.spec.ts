import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { DLPRule } from '../api/types'
import { useAuthStore } from '../stores/auth'
import DLPRulesView from './DLPRulesView.vue'

const phoneRule: DLPRule = {
  id: 'rule-phone',
  name: '手机号',
  description: '中国大陆 11 位手机号',
  category: 'pii',
  match_type: 'regex',
  pattern: '\\b1[3-9]\\d{9}\\b',
  action: 'mask',
  replacement: '***',
  priority: 20,
  enabled: true,
  builtin: true,
  alert_enabled: true,
  created_at: '2026-11-02T00:00:00Z',
  updated_at: '2026-11-02T00:00:00Z',
}

function response(data: unknown): Response {
  return {
    ok: true,
    status: 200,
    json: vi.fn().mockResolvedValue({ data, error: null }),
  } as unknown as Response
}

function mountView() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const auth = useAuthStore(pinia)
  auth.user = {
    id: 'admin-1',
    username: 'admin',
    role: 'super_admin',
    status: 1,
    created_at: '2026-11-02T00:00:00Z',
  }
  auth.initialized = true
  return mount(DLPRulesView, {
    global: {
      plugins: [pinia, ElementPlus],
      stubs: { teleport: true },
    },
  })
}

describe('DLPRulesView', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.endsWith('/dlp-rules/test')) {
        return Promise.resolve(
          response({
            action: 'mask',
            hits: [
              {
                rule_id: phoneRule.id,
                rule_name: phoneRule.name,
                action: 'mask',
                alert_enabled: true,
                start: 4,
                end: 15,
              },
            ],
            masked_text: '手机号：***',
          }),
        )
      }
      return Promise.resolve(response({ items: [phoneRule] }))
    })
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders masked DLP test result without leaking original sensitive value', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="dlp-sample"]').setValue('13800138000')
    await wrapper.find('[data-test="dlp-test"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('***')
    expect(wrapper.text()).not.toContain('13800138000')

    const testCall = fetchMock.mock.calls.find((call) =>
      String(call[0]).endsWith('/dlp-rules/test'),
    )
    expect(testCall).toBeDefined()
    expect(JSON.parse(String((testCall?.[1] as RequestInit).body))).toEqual({
      text: '13800138000',
    })
    expect(wrapper.find('[data-test="dlp-masked"]').text()).toBe('手机号：***')
  })
})
