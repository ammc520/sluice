<script setup lang="ts">
import { computed } from 'vue'
import { Lock, SwitchButton } from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'

import { usePortalAuthStore } from '../stores/portalAuth'

const portal = usePortalAuthStore()
const router = useRouter()

const employee = computed(() => portal.employee)

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
  <div class="profile-page">
    <section class="page-heading">
      <div>
        <h2>个人信息</h2>
        <p>门户账号与员工档案关联。如需更正资料，请联系管理员。</p>
      </div>
    </section>

    <el-card class="profile-card" shadow="never">
      <div class="identity">
        <span class="avatar">{{ (employee?.name ?? 'E').slice(0, 1) }}</span>
        <div>
          <strong>{{ employee?.name ?? '—' }}</strong>
          <span>{{ employee?.employee_no ?? '—' }}</span>
        </div>
      </div>

      <dl class="details">
        <div>
          <dt>工号</dt>
          <dd>{{ employee?.employee_no ?? '—' }}</dd>
        </div>
        <div>
          <dt>姓名</dt>
          <dd>{{ employee?.name ?? '—' }}</dd>
        </div>
        <div>
          <dt>邮箱</dt>
          <dd>{{ employee?.email || '未填写' }}</dd>
        </div>
        <div>
          <dt>部门</dt>
          <dd>
            {{ employee?.department_id ? `部门 #${employee.department_id}` : '未分配' }}
          </dd>
        </div>
      </dl>

      <div class="actions">
        <el-button :icon="Lock" @click="router.push('/portal/password')">
          修改登录密码
        </el-button>
        <el-button :icon="SwitchButton" @click="logout">退出登录</el-button>
      </div>
    </el-card>
  </div>
</template>

<style scoped>
.page-heading {
  margin-bottom: 20px;
}

.page-heading h2 {
  margin: 0 0 7px;
  color: var(--gw-text);
  font-size: 23px;
  font-weight: 650;
  letter-spacing: -0.02em;
}

.page-heading p {
  margin: 0;
  color: var(--gw-text-muted);
  font-size: 13px;
}

.profile-card {
  max-width: 720px;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.profile-card :deep(.el-card__body) {
  padding: 26px 28px;
}

.identity {
  display: flex;
  align-items: center;
  gap: 14px;
  padding-bottom: 22px;
  border-bottom: 1px solid #f0f4f9;
}

.avatar {
  display: grid;
  place-items: center;
  width: 52px;
  height: 52px;
  border-radius: 50%;
  background: linear-gradient(135deg, #edf3ff, #dbe7ff);
  color: var(--gw-primary);
  font-size: 19px;
  font-weight: 700;
}

.identity div {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.identity strong {
  color: var(--gw-text);
  font-size: 16px;
}

.identity span {
  color: var(--gw-text-muted);
  font-size: 12px;
}

.details {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 18px 24px;
  margin: 22px 0;
}

.details dt {
  color: #9aa7b8;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

.details dd {
  margin: 4px 0 0;
  color: #26384d;
  font-size: 13px;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  padding-top: 18px;
  border-top: 1px solid #f0f4f9;
}

@media (max-width: 560px) {
  .details {
    grid-template-columns: 1fr;
  }

  .profile-card :deep(.el-card__body) {
    padding: 22px 20px;
  }
}
</style>
