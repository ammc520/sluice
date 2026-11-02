import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'

import AdminLayout from '../layouts/AdminLayout.vue'
import { pinia } from '../stores'
import { useAuthStore } from '../stores/auth'
import DashboardView from '../views/DashboardView.vue'
import LoginView from '../views/LoginView.vue'
import PlaceholderView from '../views/PlaceholderView.vue'

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
        component: PlaceholderView,
        props: {
          title: '员工管理',
          description: '维护员工档案、状态与访问策略。',
        },
      },
      {
        path: 'tokens',
        name: 'tokens',
        component: PlaceholderView,
        props: {
          title: 'Token 管理',
          description: '签发、轮换与吊销员工访问 Token。',
        },
      },
      {
        path: 'providers',
        name: 'providers',
        component: PlaceholderView,
        props: {
          title: '上游服务',
          description: '配置 OpenAI、Anthropic、DeepSeek 等上游凭据。',
        },
      },
      {
        path: 'model-routes',
        name: 'model-routes',
        component: PlaceholderView,
        props: {
          title: '模型路由',
          description: '将模型名称映射到指定上游服务。',
        },
      },
      {
        path: 'dlp-rules',
        name: 'dlp-rules',
        component: PlaceholderView,
        props: {
          title: 'DLP 规则',
          description: '管理敏感数据识别规则与处置动作。',
        },
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
