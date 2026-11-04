import { ADMIN_HOME, ADMIN_LOGIN, ADMIN_ROOT } from './paths'

/** Returns a router-internal path for post-login navigation. Anything that
 * could be interpreted as an external or protocol-relative URL falls back to
 * the supplied default. */
export function safeRedirect(
  value: unknown,
  fallback = ADMIN_HOME,
): string {
  if (
    typeof value === 'string' &&
    value.startsWith('/') &&
    !value.startsWith('//') &&
    !value.includes('\\')
  ) {
    return value
  }
  return fallback
}

/** Admin redirects stay under /admin and never bounce back to the login page,
 * which would create a loop after a failed sign-in. */
export function safeAdminRedirect(
  value: unknown,
  fallback = ADMIN_HOME,
): string {
  const target = safeRedirect(value, fallback)
  if (
    target === ADMIN_LOGIN ||
    target.startsWith(`${ADMIN_LOGIN}?`) ||
    target === ADMIN_ROOT
  ) {
    return fallback
  }
  return target === ADMIN_ROOT || target.startsWith(`${ADMIN_ROOT}/`)
    ? target
    : fallback
}

/** Portal redirects stay inside /portal and never bounce back to the login
 * page, which would create a loop after a failed sign-in. */
export function safePortalRedirect(value: unknown): string {
  const target = safeRedirect(value, '/portal/overview')
  if (
    target === '/portal/login' ||
    target.startsWith('/portal/login?') ||
    target === '/portal'
  ) {
    return '/portal/overview'
  }
  return target.startsWith('/portal/') ? target : '/portal/overview'
}
