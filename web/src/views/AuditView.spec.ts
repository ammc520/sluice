import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { AuditLog } from '../api/types'
import { useAuthStore } from '../stores/auth'
import AuditView from './AuditView.vue'

const record: AuditLog = {
  id: 42,
  event_time: '2026-11-02T16:00:00Z',
  request_id: 'req-42',
  token_id: 'token-1',
  employee_id: 'employee-1',
  department_id: 1,
  client_ip: '10.0.0.8',
  method: 'POST',
  path: '/v1/chat/completions',
  model: 'gpt-4o',
  provider_id: 'provider-1',
  status_code: 200,
  error_code: '',
  latency_ms: 320,
  upstream_latency_ms: 280,
  first_byte_ms: 120,
  prompt_tokens: 100,
  completion_tokens: 40,
  total_tokens: 140,
  usage_estimated: false,
  has_prompt: true,
  has_response: true,
  prompt_truncated: false,
  response_truncated: false,
  dlp_action: 'mask',
  dlp_hits: [
    {
      rule_id: 'rule-phone',
      rule_name: '手机号',
      action: 'mask',
    },
  ],
  client_disconnected: false,
  upstream_request_id: 'upstream-42',
  created_at: '2026-11-02T16:00:00Z',
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
  return mount(AuditView, {
    global: {
      plugins: [pinia, ElementPlus],
      stubs: { teleport: true },
    },
  })
}

describe('AuditView', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url.includes('/employees?')) {
        return Promise.resolve(
          response({ items: [], total: 0, page: 1, page_size: 200 }),
        )
      }
      if (url.includes('/reveal')) {
        return Promise.resolve(
          response({
            prompt: JSON.stringify({
              system: '系统规则',
              messages: [{ role: 'user', content: '你好' }],
            }),
            response: JSON.stringify({
              type: 'message',
              content: [{ type: 'text', text: '世界' }],
              usage: { input_tokens: 12, output_tokens: 3 },
            }),
          }),
        )
      }
      return Promise.resolve(response({ items: [record], next_cursor: '' }))
    })
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('requires a reason before revealing audit content', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="reveal"]').trigger('click')
    await flushPromises()

    const confirm = wrapper.find('[data-test="reveal-confirm"]')
    expect(confirm.attributes('disabled')).toBeDefined()

    await wrapper.find('[data-test="reveal-reason"]').setValue('customer complaint')
    await flushPromises()

    expect(wrapper.find('[data-test="reveal-confirm"]').attributes('disabled')).toBeUndefined()
  })

  it('renders structured plaintext and hides the confirm action afterwards', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="reveal"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="reveal-reason"]').setValue('customer complaint')
    await wrapper.find('[data-test="reveal-confirm"]').trigger('click')
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('系统规则')
    expect(text).toContain('你好')
    expect(text).toContain('世界')
    expect(text).toContain('输入 12')
    // The audited request must not be repeated after a successful reveal.
    expect(wrapper.find('[data-test="reveal-confirm"]').exists()).toBe(false)
    const revealCalls = fetchMock.mock.calls.filter(([url]) =>
      String(url).includes('/reveal'),
    )
    expect(revealCalls).toHaveLength(1)
  })
})
