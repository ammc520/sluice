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

export type Protocol = 'openai_compatible' | 'anthropic'

export interface Provider {
  id: string
  name: string
  protocol: Protocol
  base_url: string
  api_key_last4: string
  is_default: boolean
  enabled: boolean
  timeout_seconds: number
  created_at: string
  updated_at: string
}

export interface ProviderPayload {
  name: string
  protocol: Protocol
  base_url: string
  api_key: string
  is_default: boolean
  enabled: boolean
  timeout_seconds: number
}

export interface ProviderTestResult {
  reachable: boolean
  status_code?: number
  message?: string
}

export type RouteMatchType = 'exact' | 'prefix' | 'glob'

export interface ModelRoute {
  id: string
  provider_id: string
  protocol: Protocol
  pattern: string
  match_type: RouteMatchType
  upstream_model: string | null
  priority: number
  enabled: boolean
  created_at: string
}

export interface ModelRoutePayload {
  provider_id: string
  protocol: Protocol
  pattern: string
  match_type: RouteMatchType
  upstream_model: string
  priority: number
  enabled: boolean
}

export type DLPMatchType = 'regex' | 'keyword'
export type DLPAction = 'block' | 'mask' | 'audit'

export interface DLPRule {
  id: string
  name: string
  description: string
  category: string
  match_type: DLPMatchType
  pattern: string
  action: DLPAction
  replacement: string
  priority: number
  enabled: boolean
  builtin: boolean
  alert_enabled: boolean
  created_at: string
  updated_at: string
}

export interface DLPRulePayload {
  name: string
  description: string
  category: string
  match_type: DLPMatchType
  pattern: string
  action: DLPAction
  replacement: string
  priority: number
  enabled: boolean
  alert_enabled: boolean
}

export interface DLPHit {
  rule_id: string
  rule_name: string
  action: DLPAction
  alert_enabled: boolean
  start: number
  end: number
}

export interface DLPTestResult {
  action: DLPAction | 'none'
  hits: DLPHit[]
  masked_text: string
}
