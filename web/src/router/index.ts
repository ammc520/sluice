import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'

import AdminLayout from '../layouts/AdminLayout.vue'
import { pinia } from '../stores'
import { useAuthStore } from '../stores/auth'
import DashboardView from '../views/DashboardView.vue'
import DLPRulesView from '../views/DLPRulesView.vue'
import EmployeesView from '../views/EmployeesView.vue'
import LoginView from '../views/LoginView.vue'
import ModelRoutesView from '../views/ModelRoutesView.vue'
import PlaceholderView from '../views/PlaceholderView.vue'
import ProvidersView from '../views/ProvidersView.vue'
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
        component: PlaceholderView,
        props: {
          title: '审计日志',
          description: '检索 API 调用、DLP 命中与处置记录。',
        },
      },
      {
        path: 'settings',
        name: 'settings',
        component: PlaceholderView,
        meta: { superOnly: true },
        props: {
          title: '系统设置',
          description: '配置邮件告警与审计日志保留策略。',
        },
      },
      {
        path: 'admin-users',
        name: 'admin-users',
        component: PlaceholderView,
        meta: { superOnly: true },
        props: {
          title: '管理员账号',
          description: '管理后台账号、角色与密码。',
        },
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
