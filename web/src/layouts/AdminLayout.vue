<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  Connection,
  Document,
  Key,
  Lock,
  Menu as MenuIcon,
  Odometer,
  Setting,
  Share,
  SwitchButton,
  Tickets,
  User,
  UserFilled,
} from '@element-plus/icons-vue'

import type { AdminRole } from '../api/types'
import { ADMIN_HOME, ADMIN_LOGIN, ADMIN_ROOT } from '../router/paths'
import { useAuthStore } from '../stores/auth'

interface NavItem {
  path: string
  label: string
  icon: unknown
  superOnly?: boolean
}

interface NavGroup {
  label: string
  items: NavItem[]
}

const adminPath = (name: string): string => `${ADMIN_ROOT}/${name}`

const navGroups: NavGroup[] = [
  {
    label: '监控',
    items: [{ path: ADMIN_HOME, label: '概览', icon: Odometer }],
  },
  {
    label: '资源',
    items: [
      { path: adminPath('employees'), label: '团队管理', icon: User },
      { path: adminPath('tokens'), label: 'Token 管理', icon: Key },
      { path: adminPath('token-requests'), label: 'Token 申请', icon: Tickets },
      { path: adminPath('providers'), label: '上游服务', icon: Connection },
      { path: adminPath('model-routes'), label: '模型路由', icon: Share },
    ],
  },
  {
    label: '安全与合规',
    items: [
      { path: adminPath('dlp-rules'), label: 'DLP 规则', icon: Lock },
      { path: adminPath('audit'), label: '审计日志', icon: Document },
    ],
  },
  {
    label: '系统',
    items: [
      { path: adminPath('settings'), label: '系统设置', icon: Setting, superOnly: true },
      {
        path: adminPath('admin-users'),
        label: '管理员账号',
        icon: UserFilled,
        superOnly: true,
      },
    ],
  },
]

const roleLabels: Record<AdminRole, string> = {
  super_admin: '超级管理员',
  operator: '操作员',
  auditor: '审计员',
}

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const sidebarOpen = ref(false)

const visibleGroups = computed(() =>
  navGroups
    .map((group) => ({
      ...group,
      items: group.items.filter(
        (item) => !item.superOnly || auth.isSuperAdmin,
      ),
    }))
    .filter((group) => group.items.length > 0),
)

const currentTitle = computed(() => {
  for (const group of navGroups) {
    const match = group.items.find((item) => item.path === route.path)
    if (match) return match.label
  }
  return '管理后台'
})

const roleLabel = computed(() =>
  auth.user ? roleLabels[auth.user.role] : '',
)

const username = computed(() => auth.user?.username ?? '')

const userInitial = computed(() =>
  (auth.user?.username ?? 'A').slice(0, 1).toUpperCase(),
)

const dateLabel = new Intl.DateTimeFormat('zh-CN', {
  month: 'long',
  day: 'numeric',
  weekday: 'long',
  timeZone: 'Asia/Shanghai',
}).format(new Date())

watch(
  () => route.path,
  () => {
    sidebarOpen.value = false
  },
)

async function logout(): Promise<void> {
  try {
    await auth.logout()
  } catch {
    auth.clear()
  } finally {
    await router.replace({ path: ADMIN_LOGIN })
  }
}
</script>

<template>
  <div class="admin-shell">
    <div
      v-if="sidebarOpen"
      class="sidebar-scrim"
      aria-hidden="true"
      @click="sidebarOpen = false"
    />

    <aside class="sidebar" :class="{ 'is-open': sidebarOpen }">
      <div class="brand">
        <div class="brand-mark">API</div>
        <div class="brand-text">
          <strong>安全网关</strong>
          <span>API · DLP 控制台</span>
        </div>
      </div>

      <nav class="nav-area" aria-label="主导航">
        <div v-for="group in visibleGroups" :key="group.label" class="nav-group">
          <span class="nav-group-label">{{ group.label }}</span>
          <el-menu
            :default-active="route.path"
            router
            class="nav-menu"
          >
            <el-menu-item
              v-for="item in group.items"
              :key="item.path"
              :index="item.path"
            >
              <el-icon><component :is="item.icon" /></el-icon>
              <span>{{ item.label }}</span>
            </el-menu-item>
          </el-menu>
        </div>
      </nav>

      <div class="sidebar-footer">
        <span class="footer-icon"><el-icon><Lock /></el-icon></span>
        <div>
          <strong>管理员操作留痕</strong>
          <span>敏感信息查看均写入审计日志</span>
        </div>
      </div>
    </aside>

    <div class="main-shell">
      <header class="topbar">
        <div class="topbar-left">
          <button
            class="menu-button"
            type="button"
            aria-label="展开导航"
            @click="sidebarOpen = true"
          >
            <el-icon><MenuIcon /></el-icon>
          </button>
          <div class="title-block">
            <span class="title-eyebrow">安全网关 · 管理控制台</span>
            <h1 class="page-title">{{ currentTitle }}</h1>
          </div>
        </div>

        <div class="topbar-right">
          <span class="date-label">{{ dateLabel }}</span>
          <span class="topbar-divider" />
          <div class="user-chip">
            <span class="avatar">{{ userInitial }}</span>
            <div class="user-text">
              <strong>{{ username }}</strong>
              <span>{{ roleLabel }}</span>
            </div>
          </div>
          <el-button text class="logout-button" @click="logout">
            <el-icon><SwitchButton /></el-icon>
            <span>退出</span>
          </el-button>
        </div>
      </header>

      <main class="content">
        <div class="content-inner">
          <router-view />
        </div>
      </main>
    </div>
  </div>
</template>

<style scoped>
.admin-shell {
  display: flex;
  min-height: 100vh;
  background: transparent;
}

.sidebar {
  position: sticky;
  top: 0;
  display: flex;
  flex: 0 0 240px;
  flex-direction: column;
  width: 240px;
  height: 100vh;
  border-right: 1px solid var(--gw-border);
  background:
    linear-gradient(180deg, rgba(47, 107, 255, 0.035), transparent 22%),
    #ffffff;
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  height: 76px;
  padding: 0 22px;
  border-bottom: 1px solid #f0f3f8;
}

.brand-mark {
  display: grid;
  flex-shrink: 0;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 12px;
  background: linear-gradient(135deg, #2f6bff, #5b8cff);
  box-shadow: 0 8px 18px -10px rgba(47, 107, 255, 0.8);
  color: #fff;
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.6px;
}

.brand-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.brand-text strong {
  color: var(--gw-text);
  font-size: 15px;
  letter-spacing: 0.3px;
}

.brand-text span {
  color: var(--gw-text-muted);
  font-size: 11px;
  letter-spacing: 0.3px;
}

.nav-area {
  flex: 1;
  padding: 16px 12px;
  overflow-y: auto;
}

.nav-group + .nav-group {
  margin-top: 18px;
}

.nav-group-label {
  display: block;
  padding: 0 12px 6px;
  color: #9aa7b8;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.12em;
}

.nav-menu {
  border-right: none;
  background: transparent;
}

.nav-menu :deep(.el-menu-item) {
  position: relative;
  height: 42px;
  margin-bottom: 2px;
  padding: 0 12px !important;
  border-radius: 9px;
  color: var(--gw-text-secondary);
  font-size: 13px;
  font-weight: 500;
}

.nav-menu :deep(.el-menu-item .el-icon) {
  color: #8b98aa;
  font-size: 17px;
  transition: color 0.15s ease;
}

.nav-menu :deep(.el-menu-item:hover) {
  background: #f5f8fd;
  color: var(--gw-text);
}

.nav-menu :deep(.el-menu-item.is-active) {
  background: var(--gw-primary-soft);
  color: var(--gw-primary);
  font-weight: 600;
}

.nav-menu :deep(.el-menu-item.is-active::before) {
  position: absolute;
  top: 10px;
  bottom: 10px;
  left: 0;
  width: 3px;
  border-radius: 0 3px 3px 0;
  background: var(--gw-primary);
  content: '';
}

.nav-menu :deep(.el-menu-item.is-active .el-icon) {
  color: var(--gw-primary);
}

.sidebar-footer {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin: 12px;
  padding: 14px;
  border: 1px solid #eaf0f8;
  border-radius: 12px;
  background: #f8faff;
}

.footer-icon {
  display: grid;
  flex-shrink: 0;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: #e9f0ff;
  color: var(--gw-primary);
  font-size: 14px;
}

.sidebar-footer div {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.sidebar-footer strong {
  color: #3d4c63;
  font-size: 11px;
  font-weight: 600;
}

.sidebar-footer span {
  color: #93a0b2;
  font-size: 10px;
  line-height: 1.45;
}

.main-shell {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}

.topbar {
  position: sticky;
  top: 0;
  z-index: 20;
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 76px;
  padding: 0 32px;
  border-bottom: 1px solid rgba(229, 235, 243, 0.9);
  background: rgba(255, 255, 255, 0.86);
  backdrop-filter: blur(14px);
}

.topbar-left,
.topbar-right {
  display: flex;
  align-items: center;
}

.topbar-left {
  gap: 12px;
  min-width: 0;
}

.title-block {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.title-eyebrow {
  color: #9aa7b8;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.page-title {
  margin: 0;
  color: var(--gw-text);
  font-size: 17px;
  font-weight: 650;
  letter-spacing: -0.01em;
}

.topbar-right {
  gap: 14px;
}

.date-label {
  color: var(--gw-text-muted);
  font-size: 12px;
  white-space: nowrap;
}

.topbar-divider {
  width: 1px;
  height: 24px;
  background: var(--gw-border);
}

.user-chip {
  display: flex;
  align-items: center;
  gap: 9px;
}

.avatar {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  background: linear-gradient(135deg, #edf3ff, #dbe7ff);
  color: var(--gw-primary);
  font-size: 12px;
  font-weight: 700;
}

.user-text {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.user-text strong {
  color: #2f3f57;
  font-size: 12px;
  font-weight: 600;
}

.user-text span {
  color: #93a0b2;
  font-size: 10px;
}

.logout-button {
  margin-left: 2px;
  color: #6b7a90;
}

.logout-button:hover {
  color: var(--gw-danger);
  background: #fdf2f2;
}

.menu-button {
  display: none;
  place-items: center;
  width: 36px;
  height: 36px;
  border: 1px solid var(--gw-border);
  border-radius: 9px;
  background: #fff;
  color: var(--gw-text-secondary);
  cursor: pointer;
}

.content {
  flex: 1;
  padding: 26px 32px 48px;
}

.content-inner {
  width: 100%;
  max-width: 1560px;
  margin: 0 auto;
}

.sidebar-scrim {
  display: none;
}

@media (max-width: 1080px) {
  .sidebar {
    position: fixed;
    z-index: 60;
    transform: translateX(-100%);
    transition: transform 0.22s ease;
    box-shadow: 18px 0 40px rgba(23, 32, 51, 0.12);
  }

  .sidebar.is-open {
    transform: translateX(0);
  }

  .sidebar-scrim {
    position: fixed;
    z-index: 55;
    display: block;
    inset: 0;
    background: rgba(20, 30, 48, 0.3);
    backdrop-filter: blur(1px);
  }

  .menu-button {
    display: grid;
  }

  .topbar {
    padding: 0 20px;
  }

  .content {
    padding: 22px 20px 40px;
  }
}

@media (max-width: 680px) {
  .date-label,
  .topbar-divider,
  .user-text {
    display: none;
  }

  .topbar {
    height: 68px;
  }

  .logout-button span {
    display: none;
  }

  .content {
    padding: 18px 14px 36px;
  }
}
</style>
