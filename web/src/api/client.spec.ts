import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError, api, setUnauthorizedHandler } from './client'

function response(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: vi.fn().mockResolvedValue(body),
  } as unknown as Response
}

function clearCookies(): void {
  for (const part of document.cookie.split(';')) {
    const name = part.split('=')[0]?.trim()
    if (name) {
      document.cookie = `${name}=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/`
    }
  }
}

describe('api client', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    setUnauthorizedHandler(null)
    clearCookies()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    setUnauthorizedHandler(null)
    clearCookies()
  })

  it('sends credentialed GET requests and unwraps the data envelope', async () => {
    document.cookie = 'csrf_token=csrf-123'
    fetchMock.mockResolvedValue(
      response(200, { data: { user: { username: 'admin' } }, error: null }),
    )

    const result = await api.get<{ user: { username: string } }>('/auth/me')

    expect(result).toEqual({ user: { username: 'admin' } })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/admin/api/auth/me')
    expect(init.method).toBe('GET')
    expect(init.credentials).toBe('include')
    expect(init.headers).toEqual({ Accept: 'application/json' })
    expect(init.body).toBeUndefined()
  })

  it('adds the CSRF header and JSON body for unsafe methods', async () => {
    document.cookie = 'csrf_token=csrf-456'
    fetchMock.mockResolvedValue(response(200, { data: { ok: true }, error: null }))

    await api.post('/auth/login', { username: 'admin', password: 'secret' })

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/admin/api/auth/login')
    expect(init.method).toBe('POST')
    expect(init.headers).toEqual({
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-CSRF-Token': 'csrf-456',
    })
    expect(init.body).toBe(JSON.stringify({ username: 'admin', password: 'secret' }))
  })

  it('throws ApiError with the server code and message', async () => {
    fetchMock.mockResolvedValue(
      response(403, {
        data: null,
        error: { code: 'forbidden', message: 'insufficient role' },
      }),
    )

    await expect(api.get('/providers')).rejects.toMatchObject({
      name: 'ApiError',
      status: 403,
      code: 'forbidden',
      message: 'insufficient role',
    })
  })

  it('notifies the unauthorized handler only for 401 responses', async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)

    fetchMock.mockResolvedValueOnce(
      response(401, {
        data: null,
        error: { code: 'unauthorized', message: 'authentication required' },
      }),
    )
    await expect(api.get('/auth/me')).rejects.toBeInstanceOf(ApiError)
    expect(handler).toHaveBeenCalledTimes(1)

    fetchMock.mockResolvedValueOnce(
      response(500, {
        data: null,
        error: { code: 'internal_error', message: 'internal server error' },
      }),
    )
    await expect(api.get('/auth/me')).rejects.toBeInstanceOf(ApiError)
    expect(handler).toHaveBeenCalledTimes(1)
  })

  it('falls back to a generic error for non-JSON failures', async () => {
    fetchMock.mockResolvedValue({
      ok: false,
      status: 502,
      json: vi.fn().mockRejectedValue(new Error('not json')),
    } as unknown as Response)

    await expect(api.get('/providers')).rejects.toMatchObject({
      status: 502,
      code: 'request_failed',
      message: '请求失败（HTTP 502）',
    })
  })
})
