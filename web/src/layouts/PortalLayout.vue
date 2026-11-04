<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  DataLine,
  Document,
  Key,
  Odometer,
  SwitchButton,
  User,
} from '@element-plus/icons-vue'

import { usePortalAuthStore } from '../stores/portalAuth'

const navItems = [
  { path: '/portal/overview', label: '概览', icon: Odometer },
  { path: '/portal/usage', label: '用量明细', icon: DataLine },
  { path: '/portal/tokens', label: '我的 Key', icon: Key },
  { path: '/portal/requests', label: '额度提额', icon: Document },
  { path: '/portal/profile', label: '个人信息', icon: User },
]

const portal = usePortalAuthStore()
const route = useRoute()
const router = useRouter()

const employee = computed(() => portal.employee)
const userInitial = computed(() =>
  (portal.employee?.name ?? 'E').slice(0, 1).toUpperCase(),
)

const currentTitle = computed(
  () => navItems.find((item) => item.path === route.path)?.label ?? '员工门户',
)

async function logout(): Promise<void> {
  try {
    await portal.logout()
  } catch {
    portal.clear()
  } finally {
    await router.replace({ path: '/portal/login' })
  }
}
</script>

<template>
  <div class="portal-shell">
    <header class="portal-header">
      <div class="header-inner">
        <router-link class="brand" to="/portal/overview">
          <span class="brand-mark">S</span>
          <span class="brand-text">
            <strong>Sluice</strong>
            <span>员工自助门户</span>
          </span>
        </router-link>

        <el-menu
          :default-active="route.path"
          class="portal-nav"
          mode="horizontal"
          router
          :ellipsis="false"
        >
          <el-menu-item
            v-for="item in navItems"
            :key="item.path"
            :index="item.path"
          >
            <el-icon><component :is="item.icon" /></el-icon>
            <span>{{ item.label }}</span>
          </el-menu-item>
        </el-menu>

        <div class="header-user">
          <span class="avatar">{{ userInitial }}</span>
          <span class="user-text">
            <strong>{{ employee?.name ?? '' }}</strong>
            <span>{{ employee?.employee_no ?? '' }}</span>
          </span>
          <el-button text class="logout-button" @click="logout">
            <el-icon><SwitchButton /></el-icon>
            <span>退出</span>
          </el-button>
        </div>
      </div>
    </header>

    <main class="portal-main">
      <div class="portal-content">
        <div class="page-eyebrow">员工门户 · {{ currentTitle }}</div>
        <router-view />
      </div>
    </main>
  </div>
</template>

<style scoped>
.portal-shell {
  min-height: 100vh;
}

.portal-header {
  position: sticky;
  top: 0;
  z-index: 20;
  border-bottom: 1px solid rgba(229, 235, 243, 0.9);
  background: rgba(255, 255, 255, 0.9);
  backdrop-filter: blur(14px);
}

.header-inner {
  display: flex;
  align-items: center;
  gap: 26px;
  width: 100%;
  max-width: 1240px;
  height: 68px;
  margin: 0 auto;
  padding: 0 28px;
}

.brand {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 10px;
  text-decoration: none;
}

.brand-mark {
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  border-radius: 11px;
  background: linear-gradient(135deg, #2f6bff, #5b8cff);
  box-shadow: 0 8px 18px -10px rgba(47, 107, 255, 0.8);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.6px;
}

.brand-text {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.brand-text strong {
  color: var(--gw-text);
  font-size: 14px;
}

.brand-text span {
  color: var(--gw-text-muted);
  font-size: 10px;
}

.portal-nav {
  flex: 1;
  min-width: 0;
  border-bottom: none;
  background: transparent;
}

.portal-nav :deep(.el-menu-item) {
  height: 68px;
  gap: 6px;
  padding: 0 14px;
  border-bottom: 2px solid transparent;
  color: var(--gw-text-secondary);
  font-size: 13px;
}

.portal-nav :deep(.el-menu-item.is-active) {
  border-bottom-color: var(--gw-primary);
  color: var(--gw-primary);
  font-weight: 600;
}

.header-user {
  display: flex;
  flex-shrink: 0;
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
  color: #6b7a90;
}

.logout-button:hover {
  background: #fdf2f2;
  color: var(--gw-danger);
}

.portal-main {
  padding: 28px 28px 56px;
}

.portal-content {
  width: 100%;
  max-width: 1240px;
  margin: 0 auto;
}

.page-eyebrow {
  margin-bottom: 10px;
  color: #9aa7b8;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.1em;
  text-transform: uppercase;
}

@media (max-width: 860px) {
  .header-inner {
    flex-wrap: wrap;
    height: auto;
    padding: 12px 18px;
  }

  .portal-nav {
    order: 3;
    flex-basis: 100%;
  }

  .portal-nav :deep(.el-menu-item) {
    height: 42px;
    padding: 0 10px;
    font-size: 12px;
  }

  .header-user {
    margin-left: auto;
  }

  .portal-main {
    padding: 20px 16px 44px;
  }
}

@media (max-width: 560px) {
  .user-text {
    display: none;
  }

  .brand-text span {
    display: none;
  }
}
</style>
