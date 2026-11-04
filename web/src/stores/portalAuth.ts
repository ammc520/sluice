import { defineStore } from 'pinia'

import { ApiError, portalApi } from '../api/client'
import type {
  PortalEmployee,
  PortalLoginResponse,
  PortalMeResponse,
  PortalPasswordChangeResult,
} from '../api/types'

interface PortalAuthState {
  employee: PortalEmployee | null
  mustChangePassword: boolean
  initialized: boolean
  loading: boolean
}

export const usePortalAuthStore = defineStore('portalAuth', {
  state: (): PortalAuthState => ({
    employee: null,
    mustChangePassword: false,
    initialized: false,
    loading: false,
  }),

  getters: {
    isAuthenticated: (state): boolean => state.employee !== null,
  },

  actions: {
    async me(): Promise<PortalEmployee | null> {
      this.loading = true
      try {
        const response = await portalApi.get<PortalMeResponse>('/auth/me')
        this.employee = response.employee
        this.mustChangePassword = response.must_change_password
        return this.employee
      } catch (error) {
        this.employee = null
        this.mustChangePassword = false
        if (
          error instanceof ApiError &&
          (error.status === 401 ||
            error.status === 403 ||
            error.status === 404)
        ) {
          return null
        }
        throw error
      } finally {
        this.loading = false
        this.initialized = true
      }
    },

    async login(
      employeeNo: string,
      password: string,
    ): Promise<PortalLoginResponse> {
      const response = await portalApi.post<PortalLoginResponse>(
        '/auth/login',
        { employee_no: employeeNo, password },
      )
      this.employee = response.employee
      this.mustChangePassword = response.must_change_password
      this.initialized = true
      return response
    },

    async changePassword(
      currentPassword: string,
      newPassword: string,
    ): Promise<PortalPasswordChangeResult> {
      const response = await portalApi.post<PortalPasswordChangeResult>(
        '/auth/change-password',
        {
          current_password: currentPassword,
          new_password: newPassword,
        },
      )
      this.clear()
      return response
    },

    async logout(): Promise<void> {
      try {
        await portalApi.post<{ logged_out: boolean }>('/auth/logout')
      } finally {
        this.clear()
      }
    },

    clear(): void {
      this.employee = null
      this.mustChangePassword = false
      this.initialized = true
      this.loading = false
    },
  },
})
