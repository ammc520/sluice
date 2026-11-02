import { ApiError } from './client'

const translatedMessages: Record<string, string> = {
  'employee_no already exists': '工号已存在',
  'department does not exist': '部门不存在',
  'employee not found': '员工不存在',
  'token not found': 'Token 不存在',
  'expires_at must be in the future': '过期时间必须晚于当前时间',
  'cannot rotate an expired token; set a new expiration first':
    '已过期的 Token 无法轮换，请先调整有效期',
  'username already exists': '用户名已存在',
  'administrator not found': '管理员不存在',
  'invalid username or password': '用户名或密码错误',
  'too many login attempts': '登录尝试次数过多，请稍后再试',
}

/** Converts API failures into concise Chinese messages for the admin UI. */
export function apiErrorMessage(
  error: unknown,
  fallback = '操作失败，请稍后重试',
): string {
  if (!(error instanceof ApiError)) {
    return '网络异常，请检查连接后重试'
  }
  if (translatedMessages[error.message]) {
    return translatedMessages[error.message]
  }
  if (error.message.startsWith('invalid IP or CIDR')) {
    return 'IP 白名单格式不正确'
  }
  if (error.status === 403) {
    return '当前账号无权执行此操作'
  }
  if (error.status === 401) {
    return '登录已失效，请重新登录'
  }
  if (error.status === 404) {
    return '记录不存在或已被删除'
  }
  return error.message || fallback
}
