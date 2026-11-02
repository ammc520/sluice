export const API_PREFIX = '/admin/api'

const CSRF_COOKIE = 'csrf_token'
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

let unauthorizedHandler: UnauthorizedHandler | null = null

/** Registers the application-wide reaction to an authenticated request
 * returning 401. The handler lives outside the client so the client stays
 * free of router/store imports. */
export function setUnauthorizedHandler(handler: UnauthorizedHandler | null): void {
  unauthorizedHandler = handler
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

function buildURL(path: string): string {
  if (path.startsWith('/admin/api')) return path
  return `${API_PREFIX}${path.startsWith('/') ? path : `/${path}`}`
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (!SAFE_METHODS.has(method)) {
    const csrfToken = readCookie(CSRF_COOKIE)
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

export const api = {
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
}
