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

export interface Department {
  id: number
  name: string
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

export interface ProviderModelsResult {
  items: string[]
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

export interface DashboardOverview {
  from: string
  to: string
  total_calls: number
  active_users: number
  total_tokens: number
  dlp_hits: number
  error_calls: number
  error_rate: number
}

export interface TrendPoint {
  day: string
  calls: number
  tokens: number
}

export interface TrendResponse {
  interval: 'day'
  items: TrendPoint[]
}

export interface TopUser {
  employee_id: string
  name: string
  calls: number
  tokens: number
}

export interface DLPStat {
  rule_name: string
  action: DLPAction
  hits: number
}

export type DashboardRange = 'today' | '24h' | '7d' | '30d'
export type DashboardInterval = 'hour' | 'day'

export interface DashboardMetric {
  value: number
  previous: number
  delta_pct?: number | null
  delta_pp?: number | null
}

export interface DashboardKPIs {
  requests: DashboardMetric
  tokens: DashboardMetric
  prompt_tokens: DashboardMetric
  completion_tokens: DashboardMetric
  active_users: DashboardMetric
  error_rate: DashboardMetric
  dlp_requests: DashboardMetric
  p95_latency_ms: DashboardMetric
  client_disconnects: DashboardMetric
}

export interface DashboardTrendPoint {
  requests: number
  errors: number
  tokens: number
  prompt_tokens: number
  completion_tokens: number
  dlp_requests: number
  p95_latency_ms: number | null
}

export interface DashboardTrend {
  interval: DashboardInterval
  buckets: string[]
  current: DashboardTrendPoint[]
  previous: DashboardTrendPoint[]
}

export interface DashboardProviderStat {
  id: string
  name: string
  requests: number
  tokens: number
  errors: number
  p95_latency_ms: number
}

export interface DashboardModelStat {
  model: string
  requests: number
  tokens: number
}

export interface DashboardQuotaBrief {
  type: QuotaType
  period: QuotaPeriod
  used: number
  total: number
  pct: number
}

export interface DashboardUser {
  employee_id: string
  employee_no: string
  name: string
  department: string
  requests: number
  tokens: number
  errors: number
  error_rate: number
  dlp_requests: number
  quota: DashboardQuotaBrief | null
}

export interface DashboardDLPAction {
  action: DLPAction
  requests: number
  hits: number
}

export interface DashboardDLPRule {
  rule_name: string
  action: DLPAction
  hits: number
}

export interface DashboardDLP {
  requests: number
  hits: number
  by_action: DashboardDLPAction[]
  by_rule: DashboardDLPRule[]
}

export interface DashboardQuotaPressure {
  employee_id: string
  name: string
  department: string
  quota_type: QuotaType
  quota_period: QuotaPeriod
  used: number
  total: number
  pct: number
  period_start: string | null
  expires_at: string | null
}

export interface DashboardIncident {
  id: number
  event_time: string
  employee_id: string
  employee_name: string
  action: DLPAction
  rules: string[]
  model: string
  request_id: string
}

export interface DashboardSummaryRange {
  from: string
  to: string
  previous_from: string
  previous_to: string
  interval: DashboardInterval
  timezone: string
}

export interface DashboardSummary {
  generated_at: string
  range: DashboardSummaryRange
  kpis: DashboardKPIs
  trend: DashboardTrend
  breakdown: {
    providers: DashboardProviderStat[]
    models: DashboardModelStat[]
  }
  top_users: DashboardUser[]
  dlp: DashboardDLP
  quota_pressure: DashboardQuotaPressure[]
  recent_incidents: DashboardIncident[]
}

export interface AuditDLPHit {
  rule_id: string
  rule_name: string
  action: DLPAction
}

export interface AuditLog {
  id: number
  event_time: string
  request_id: string
  token_id: string | null
  employee_id: string | null
  department_id: number | null
  client_ip: string
  method: string
  path: string
  model: string
  provider_id: string | null
  status_code: number
  error_code: string
  latency_ms: number
  upstream_latency_ms: number
  first_byte_ms: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  usage_estimated: boolean
  has_prompt: boolean
  has_response: boolean
  prompt_truncated: boolean
  response_truncated: boolean
  dlp_action: DLPAction | 'none' | ''
  dlp_hits: AuditDLPHit[]
  client_disconnected: boolean
  upstream_request_id: string
  created_at: string
}

export interface AuditListResponse {
  items: AuditLog[]
  next_cursor: string
}

export interface AuditRevealResult {
  prompt: string
  response: string
}

export interface SMTPSettings {
  host: string
  port: number
  username: string
  password_set: boolean
  from: string
  to: string[]
  use_tls: boolean
}

export interface SMTPSettingsPayload {
  host: string
  port: number
  username: string
  password: string
  from: string
  to: string[]
  use_tls: boolean
}

export interface SMTPTestResult {
  sent: boolean
}

export interface AuditSettings {
  retention_days: number
  alert_enabled: boolean
}

export interface AuditSettingsPayload {
  retention_days: number
  alert_enabled: boolean
}

export interface AdminUserCreatePayload {
  username: string
  password: string
  role: AdminRole
  status: number
}

export interface AdminUserUpdatePayload {
  username?: string
  role?: AdminRole
  status?: number
}

export interface AdminUserListResponse {
  items: AdminUser[]
}

export interface ResetPasswordResult {
  reset: boolean
}
