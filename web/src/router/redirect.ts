/** Returns a router-internal path for post-login navigation. Anything that
 * could be interpreted as an external or protocol-relative URL falls back to
 * the dashboard. */
export function safeRedirect(value: unknown): string {
  if (
    typeof value === 'string' &&
    value.startsWith('/') &&
    !value.startsWith('//') &&
    !value.includes('\\')
  ) {
    return value
  }
  return '/dashboard'
}
