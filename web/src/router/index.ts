import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'

import AdminLayout from '../layouts/AdminLayout.vue'
import PortalLayout from '../layouts/PortalLayout.vue'
import { pinia } from '../stores'
import { useAuthStore } from '../stores/auth'
import { usePortalAuthStore } from '../stores/portalAuth'
import LoginView from '../views/LoginView.vue'
import PortalLoginView from '../views/PortalLoginView.vue'
import {
  ADMIN_HOME,
  ADMIN_LOGIN,
  ADMIN_ROOT,
  PORTAL_HOME,
  PORTAL_LOGIN,
  PORTAL_ROOT,
  isPortalPath,
} from './paths'

const routes: RouteRecordRaw[] = [
  {
    path: ADMIN_LOGIN,
    name: 'login',
    component: LoginView,
    meta: { public: true },
  },
  {
    path: PORTAL_LOGIN,
    name: 'portal-login',
    component: PortalLoginView,
    meta: { portalPublic: true },
  },
  {
    path: PORTAL_ROOT,
    component: PortalLayout,
    redirect: PORTAL_HOME,
    meta: { portal: true },
    children: [
      {
        path: 'overview',
        name: 'portal-overview',
        component: () => import('../views/PortalOverviewView.vue'),
      },
      {
        path: 'usage',
        name: 'portal-usage',
        component: () => import('../views/PortalUsageView.vue'),
      },
      {
        path: 'tokens',
        name: 'portal-tokens',
        component: () => import('../views/PortalTokensView.vue'),
      },
      {
        path: 'requests',
        name: 'portal-requests',
        component: () => import('../views/PortalRequestsView.vue'),
      },
      {
        path: 'profile',
        name: 'portal-profile',
        component: () => import('../views/PortalProfileView.vue'),
      },
      {
        path: 'password',
        name: 'portal-password',
        component: () => import('../views/PortalPasswordView.vue'),
      },
      { path: ':pathMatch(.*)*', redirect: PORTAL_HOME },
    ],
  },
  {
    path: ADMIN_ROOT,
    component: AdminLayout,
    redirect: ADMIN_HOME,
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
        path: 'token-requests',
        name: 'token-requests',
        component: () => import('../views/TokenRequestsView.vue'),
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
  { path: '/', redirect: PORTAL_LOGIN },
  { path: '/:pathMatch(.*)*', redirect: PORTAL_LOGIN },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach(async (to) => {
  if (isPortalPath(to.path)) {
    const portal = usePortalAuthStore(pinia)

    if (to.name === 'portal-login') {
      if (!portal.initialized) {
        try {
          await portal.me()
        } catch {
          portal.clear()
        }
      }
      if (portal.isAuthenticated) {
        return portal.mustChangePassword
          ? { path: '/portal/password' }
          : { path: PORTAL_HOME }
      }
      return true
    }

    if (!portal.initialized) {
      try {
        await portal.me()
      } catch {
        portal.clear()
      }
    }

    if (!portal.isAuthenticated) {
      return {
        path: PORTAL_LOGIN,
        query:
          to.fullPath === PORTAL_ROOT ? undefined : { redirect: to.fullPath },
      }
    }

    // The initial administrator-issued password must be replaced before any
    // usage or token data is shown.
    if (portal.mustChangePassword && to.name !== 'portal-password') {
      return { path: '/portal/password' }
    }

    return true
  }

  const auth = useAuthStore(pinia)

  if (!auth.initialized) {
    try {
      await auth.me()
    } catch {
      auth.clear()
    }
  }

  if (to.meta.public) {
    return auth.isAuthenticated ? { path: ADMIN_HOME } : true
  }

  if (!auth.isAuthenticated) {
    return {
      path: ADMIN_LOGIN,
      query: to.fullPath === ADMIN_ROOT ? undefined : { redirect: to.fullPath },
    }
  }

  if (to.meta.superOnly && !auth.isSuperAdmin) {
    return { path: ADMIN_HOME }
  }

  return true
})

export default router
