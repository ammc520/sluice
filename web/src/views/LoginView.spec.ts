import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import type { AdminUser } from '../api/types'
import { useAuthStore } from '../stores/auth'
import LoginView from './LoginView.vue'

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

async function mountLogin() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/login', component: LoginView },
      { path: '/dashboard', component: { template: '<div>dashboard</div>' } },
    ],
  })
  await router.push('/login')
  await router.isReady()
  const wrapper = mount(LoginView, {
    global: { plugins: [pinia, router, ElementPlus] },
  })
  return { wrapper, router, auth: useAuthStore(pinia) }
}

async function submitCredentials(
  wrapper: Awaited<ReturnType<typeof mountLogin>>['wrapper'],
  username = 'admin',
  password = 'wrong-password',
) {
  await wrapper.find('input[data-test="username"]').setValue(username)
  await wrapper.find('input[data-test="password"]').setValue(password)
  await wrapper.find('form').trigger('submit')
  await flushPromises()
}

describe('LoginView', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shows a localized message when credentials are rejected', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValue(
      response(401, {
        data: null,
        error: { code: 'invalid_credentials', message: 'invalid username or password' },
      }),
    )
    const { wrapper, router, auth } = await mountLogin()

    await submitCredentials(wrapper)

    expect(wrapper.find('[data-test="login-error"]').text()).toContain(
      '用户名或密码错误',
    )
    expect(auth.isAuthenticated).toBe(false)
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('stores the signed-in user and navigates after a successful login', async () => {
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockResolvedValue(
      response(200, {
        data: { user: adminUser, expires_in: 28800 },
        error: null,
      }),
    )
    const { wrapper, router, auth } = await mountLogin()

    await submitCredentials(wrapper, 'admin', 'correct-password')

    expect(auth.user).toEqual(adminUser)
    expect(auth.isSuperAdmin).toBe(true)
    expect(router.currentRoute.value.path).toBe('/dashboard')
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(init.body).toBe(
      JSON.stringify({ username: 'admin', password: 'correct-password' }),
    )
  })

  it('does not call the API when the form is empty', async () => {
    const fetchMock = vi.mocked(fetch)
    const { wrapper } = await mountLogin()

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(fetchMock).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="login-error"]').text()).toContain(
      '请输入用户名和密码',
    )
  })
})
