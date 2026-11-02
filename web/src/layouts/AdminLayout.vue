<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  Connection,
  Document,
  Key,
  Lock,
  Odometer,
  Setting,
  Share,
  SwitchButton,
  User,
  UserFilled,
} from '@element-plus/icons-vue'

import type { AdminRole } from '../api/types'
import { useAuthStore } from '../stores/auth'

interface NavItem {
  path: string
  label: string
  icon: unknown
  superOnly?: boolean
}

const navItems: NavItem[] = [
  { path: '/dashboard', label: '概览', icon: Odometer },
  { path: '/employees', label: '员工管理', icon: User },
  { path: '/tokens', label: 'Token 管理', icon: Key },
  { path: '/providers', label: '上游服务', icon: Connection },
  { path: '/model-routes', label: '模型路由', icon: Share },
  { path: '/dlp-rules', label: 'DLP 规则', icon: Lock },
  { path: '/audit', label: '审计日志', icon: Document },
  { path: '/settings', label: '系统设置', icon: Setting, superOnly: true },
  { path: '/admin-users', label: '管理员账号', icon: UserFilled, superOnly: true },
]

const roleLabels: Record<AdminRole, string> = {
  super_admin: '超级管理员',
  operator: '操作员',
  auditor: '审计员',
}

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const visibleNav = computed(() =>
  navItems.filter((item) => !item.superOnly || auth.isSuperAdmin),
)

const currentTitle = computed(
  () => navItems.find((item) => item.path === route.path)?.label ?? '管理后台',
)

const roleLabel = computed(() =>
  auth.user ? roleLabels[auth.user.role] : '',
)

async function logout(): Promise<void> {
  try {
    await auth.logout()
  } catch {
    auth.clear()
  } finally {
    await router.replace({ path: '/login' })
  }
}
</script>

<template>
  <el-container class="admin-shell">
    <el-aside width="220px" class="sidebar">
      <div class="brand">
        <div class="brand-mark">API</div>
        <div class="brand-text">
          <strong>安全网关</strong>
          <span>DLP 管理控制台</span>
        </div>
      </div>

      <el-menu
        :default-active="route.path"
        router
        class="nav-menu"
        background-color="#16212f"
        text-color="#b8c4d4"
        active-text-color="#ffffff"
      >
        <el-menu-item v-for="item in visibleNav" :key="item.path" :index="item.path">
          <el-icon><component :is="item.icon" /></el-icon>
          <span>{{ item.label }}</span>
        </el-menu-item>
      </el-menu>
    </el-aside>

    <el-container class="main-shell">
      <el-header class="topbar">
        <div class="page-title">{{ currentTitle }}</div>
        <div class="user-area">
          <el-icon class="user-icon"><UserFilled /></el-icon>
          <span class="username">{{ auth.user?.username }}</span>
          <el-tag size="small" effect="plain">{{ roleLabel }}</el-tag>
          <el-button text class="logout-button" @click="logout">
            <el-icon><SwitchButton /></el-icon>
            <span>退出</span>
          </el-button>
        </div>
      </el-header>

      <el-main class="content">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<style scoped>
.admin-shell {
  min-height: 100vh;
  background: #f3f6fa;
}

.sidebar {
  display: flex;
  flex-direction: column;
  background: #16212f;
  color: #fff;
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  height: 72px;
  padding: 0 20px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}

.brand-mark {
  display: grid;
  place-items: center;
  width: 38px;
  height: 38px;
  border-radius: 10px;
  background: linear-gradient(135deg, #409eff, #2f7fd4);
  color: #fff;
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.5px;
}

.brand-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.brand-text strong {
  font-size: 15px;
  letter-spacing: 0.4px;
}

.brand-text span {
  color: #7f8fa4;
  font-size: 11px;
}

.nav-menu {
  flex: 1;
  border-right: none;
  padding: 10px 8px;
}

.nav-menu .el-menu-item {
  height: 46px;
  margin-bottom: 4px;
  border-radius: 8px;
}

.nav-menu .el-menu-item.is-active {
  background: #409eff;
}

.nav-menu .el-menu-item:hover {
  background: rgba(64, 158, 255, 0.16);
}

.main-shell {
  min-width: 0;
}

.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 64px;
  padding: 0 28px;
  background: #fff;
  border-bottom: 1px solid #e6ebf2;
}

.page-title {
  color: #1f2d3d;
  font-size: 18px;
  font-weight: 600;
}

.user-area {
  display: flex;
  align-items: center;
  gap: 10px;
}

.user-icon {
  color: #7a8ba1;
  font-size: 18px;
}

.username {
  color: #26384d;
  font-size: 14px;
  font-weight: 500;
}

.logout-button {
  margin-left: 8px;
  color: #6b7c91;
}

.content {
  padding: 24px 28px 40px;
}
</style>
