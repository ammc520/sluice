import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'

import AdminLayout from '../layouts/AdminLayout.vue'
import { pinia } from '../stores'
import { useAuthStore } from '../stores/auth'
import AdminUsersView from '../views/AdminUsersView.vue'
import AuditView from '../views/AuditView.vue'
import DashboardView from '../views/DashboardView.vue'
import DLPRulesView from '../views/DLPRulesView.vue'
import EmployeesView from '../views/EmployeesView.vue'
import LoginView from '../views/LoginView.vue'
import ModelRoutesView from '../views/ModelRoutesView.vue'
import ProvidersView from '../views/ProvidersView.vue'
import SettingsView from '../views/SettingsView.vue'
import TokensView from '../views/TokensView.vue'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: LoginView,
    meta: { public: true },
  },
  {
    path: '/',
    component: AdminLayout,
    redirect: '/dashboard',
    children: [
      {
        path: 'dashboard',
        name: 'dashboard',
        component: DashboardView,
      },
      {
        path: 'employees',
        name: 'employees',
        component: EmployeesView,
      },
      {
        path: 'tokens',
        name: 'tokens',
        component: TokensView,
      },
      {
        path: 'providers',
        name: 'providers',
        component: ProvidersView,
      },
      {
        path: 'model-routes',
        name: 'model-routes',
        component: ModelRoutesView,
      },
      {
        path: 'dlp-rules',
        name: 'dlp-rules',
        component: DLPRulesView,
      },
      {
        path: 'audit',
        name: 'audit',
        component: AuditView,
      },
      {
        path: 'settings',
        name: 'settings',
        component: SettingsView,
        meta: { superOnly: true },
      },
      {
        path: 'admin-users',
        name: 'admin-users',
        component: AdminUsersView,
        meta: { superOnly: true },
      },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/dashboard' },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach(async (to) => {
  const auth = useAuthStore(pinia)

  if (!auth.initialized) {
    try {
      await auth.me()
    } catch {
      auth.clear()
    }
  }

  if (to.meta.public) {
    return auth.isAuthenticated ? { path: '/dashboard' } : true
  }

  if (!auth.isAuthenticated) {
    return {
      path: '/login',
      query: to.fullPath === '/' ? undefined : { redirect: to.fullPath },
    }
  }

  if (to.meta.superOnly && !auth.isSuperAdmin) {
    return { path: '/dashboard' }
  }

  return true
})

export default router
