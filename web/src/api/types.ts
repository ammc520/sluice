export type AdminRole = 'super_admin' | 'operator' | 'auditor'

export interface AdminUser {
  id: string
  username: string
  role: AdminRole
  status: number
  last_login_at?: string | null
  created_at: string
}

export interface MeResponse {
  user: AdminUser
}

export interface LoginResponse {
  user: AdminUser
  expires_in: number
}

export interface LogoutResponse {
  logged_out: boolean
}

export interface PageResponse<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export interface Employee {
  id: string
  employee_no: string
  name: string
  email: string
  department_id: number | null
  status: number
  created_at: string
  updated_at: string
}

export interface EmployeePayload {
  employee_no: string
  name: string
  email: string
  department_id: number | null
  status: number
}

export type QuotaType = 'tokens' | 'requests'
export type QuotaPeriod = 'daily' | 'monthly' | 'total'

export interface Token {
  id: string
  employee_id: string
  token_prefix: string
  token_last4: string
  quota_type: QuotaType
  quota_period: QuotaPeriod
  quota_total: number | null
  quota_used: number
  period_start: string | null
  rate_limit_rpm: number
  ip_allowlist: string[]
  status: number
  expires_at: string | null
  created_at: string
  last_used_at: string | null
}

export interface TokenPayload {
  employee_id: string
  quota_type: QuotaType
  quota_period: QuotaPeriod
  quota_total: number | null
  rate_limit_rpm: number
  expires_at: string | null
  ip_allowlist: string[]
}

export interface TokenCreateResult {
  token: string
  token_id: string
}
