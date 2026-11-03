import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'

import AdminLayout from '../layouts/AdminLayout.vue'
import { pinia } from '../stores'
import { useAuthStore } from '../stores/auth'
import LoginView from '../views/LoginView.vue'

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
        component: () => import('../views/DashboardView.vue'),
      },
      {
        path: 'employees',
        name: 'employees',
        component: () => import('../views/EmployeesView.vue'),
      },
      {
        path: 'tokens',
        name: 'tokens',
        component: () => import('../views/TokensView.vue'),
      },
      {
        path: 'providers',
        name: 'providers',
        component: () => import('../views/ProvidersView.vue'),
      },
      {
        path: 'model-routes',
        name: 'model-routes',
        component: () => import('../views/ModelRoutesView.vue'),
      },
      {
        path: 'dlp-rules',
        name: 'dlp-rules',
        component: () => import('../views/DLPRulesView.vue'),
      },
      {
        path: 'audit',
        name: 'audit',
        component: () => import('../views/AuditView.vue'),
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('../views/SettingsView.vue'),
        meta: { superOnly: true },
      },
      {
        path: 'admin-users',
        name: 'admin-users',
        component: () => import('../views/AdminUsersView.vue'),
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
