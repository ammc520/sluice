export const API_PREFIX = '/admin/api'
export const PORTAL_API_PREFIX = '/portal/api'

const ADMIN_CSRF_COOKIE = 'csrf_token'
const PORTAL_CSRF_COOKIE = 'employee_csrf'
const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])

export interface ApiErrorPayload {
  code?: string
  message?: string
}

interface Envelope<T> {
  data?: T | null
  error?: ApiErrorPayload | null
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

type UnauthorizedHandler = () => void

export interface ApiClientOptions {
  /** URL prefix prepended to relative paths. */
  prefix: string
  /** Double-submit CSRF cookie read for unsafe requests. */
  csrfCookie: string
}

export interface ApiClient {
  get<T>(path: string): Promise<T>
  post<T>(path: string, body?: unknown): Promise<T>
  put<T>(path: string, body?: unknown): Promise<T>
  del<T>(path: string): Promise<T>
  /** Registers the reaction to an authenticated request returning 401.
   * Each surface (admin, portal) has an independent handler. */
  setUnauthorizedHandler(handler: UnauthorizedHandler | null): void
}

export function readCookie(name: string): string | null {
  const prefix = `${name}=`
  for (const part of document.cookie.split(';')) {
    const item = part.trim()
    if (item.startsWith(prefix)) {
      return decodeURIComponent(item.slice(prefix.length))
    }
  }
  return null
}

/** Creates an API client bound to one cookie/CSRF namespace. Admin and portal
 * sessions are deliberately isolated: a portal request never reads the admin
 * CSRF cookie and vice versa. */
export function createClient(options: ApiClientOptions): ApiClient {
  let unauthorizedHandler: UnauthorizedHandler | null = null

  function buildURL(path: string): string {
    if (path.startsWith(options.prefix)) return path
    return `${options.prefix}${path.startsWith('/') ? path : `/${path}`}`
  }

  async function request<T>(
    method: string,
    path: string,
    body?: unknown,
  ): Promise<T> {
    const headers: Record<string, string> = { Accept: 'application/json' }
    if (!SAFE_METHODS.has(method)) {
      const csrfToken = readCookie(options.csrfCookie)
      if (csrfToken) headers['X-CSRF-Token'] = csrfToken
    }

    let payload: string | undefined
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json'
      payload = JSON.stringify(body)
    }

    const response = await fetch(buildURL(path), {
      method,
      credentials: 'include',
      headers,
      body: payload,
    })

    let envelope: Envelope<T> | null = null
    try {
      envelope = (await response.json()) as Envelope<T>
    } catch {
      envelope = null
    }

    if (!response.ok || envelope?.error) {
      if (response.status === 401) unauthorizedHandler?.()
      const code = envelope?.error?.code ?? 'request_failed'
      const message =
        envelope?.error?.message ?? `请求失败（HTTP ${response.status}）`
      throw new ApiError(response.status, code, message)
    }

    return envelope?.data as T
  }

  return {
    get<T>(path: string): Promise<T> {
      return request<T>('GET', path)
    },
    post<T>(path: string, body?: unknown): Promise<T> {
      return request<T>('POST', path, body)
    },
    put<T>(path: string, body?: unknown): Promise<T> {
      return request<T>('PUT', path, body)
    },
    del<T>(path: string): Promise<T> {
      return request<T>('DELETE', path)
    },
    setUnauthorizedHandler(handler: UnauthorizedHandler | null): void {
      unauthorizedHandler = handler
    },
  }
}

/** Administrator console client. */
export const api = createClient({
  prefix: API_PREFIX,
  csrfCookie: ADMIN_CSRF_COOKIE,
})

/** Employee self-service portal client. */
export const portalApi = createClient({
  prefix: PORTAL_API_PREFIX,
  csrfCookie: PORTAL_CSRF_COOKIE,
})

export const setUnauthorizedHandler = api.setUnauthorizedHandler
export const setPortalUnauthorizedHandler = portalApi.setUnauthorizedHandler
