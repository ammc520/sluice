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
