// Canonical browser paths. Keep every namespace here so layouts, guards and
// redirect handling cannot drift apart.
export const ADMIN_ROOT = '/admin'
export const ADMIN_HOME = '/admin/dashboard'
export const ADMIN_LOGIN = '/admin/login'

export const PORTAL_ROOT = '/portal'
export const PORTAL_HOME = '/portal/overview'
export const PORTAL_LOGIN = '/portal/login'

export function isPortalPath(path: string): boolean {
  return path === PORTAL_ROOT || path.startsWith(`${PORTAL_ROOT}/`)
}
