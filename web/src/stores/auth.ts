import { defineStore } from 'pinia'

import { ApiError, api } from '../api/client'
import type {
  AdminUser,
  LoginResponse,
  LogoutResponse,
  MeResponse,
} from '../api/types'

interface AuthState {
  user: AdminUser | null
  initialized: boolean
  loading: boolean
}

export const useAuthStore = defineStore('auth', {
  state: (): AuthState => ({
    user: null,
    initialized: false,
    loading: false,
  }),

  getters: {
    isAuthenticated: (state): boolean => state.user !== null,
    isSuperAdmin: (state): boolean => state.user?.role === 'super_admin',
  },

  actions: {
    async me(): Promise<AdminUser | null> {
      this.loading = true
      try {
        const response = await api.get<MeResponse>('/auth/me')
        this.user = response.user
        return this.user
      } catch (error) {
        this.user = null
        if (
          error instanceof ApiError &&
          (error.status === 401 || error.status === 403)
        ) {
          return null
        }
        throw error
      } finally {
        this.loading = false
        this.initialized = true
      }
    },

    async login(username: string, password: string): Promise<AdminUser> {
      const response = await api.post<LoginResponse>('/auth/login', {
        username,
        password,
      })
      this.user = response.user
      this.initialized = true
      return response.user
    },

    async logout(): Promise<void> {
      try {
        await api.post<LogoutResponse>('/auth/logout')
      } finally {
        this.clear()
      }
    },

    clear(): void {
      this.user = null
      this.initialized = true
      this.loading = false
    },
  },
})
