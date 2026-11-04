<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { CircleCheck, Key, Lock, User } from '@element-plus/icons-vue'

import { ApiError } from '../api/client'
import { safePortalRedirect } from '../router/redirect'
import { usePortalAuthStore } from '../stores/portalAuth'

const portal = usePortalAuthStore()
const route = useRoute()
const router = useRouter()

const form = reactive({ employeeNo: '', password: '' })
const loading = ref(false)
const errorMessage = ref('')

async function submit(): Promise<void> {
  errorMessage.value = ''
  const employeeNo = form.employeeNo.trim()
  if (!employeeNo || !form.password) {
    errorMessage.value = '请输入工号和密码'
    return
  }

  loading.value = true
  try {
    const response = await portal.login(employeeNo, form.password)
    if (response.must_change_password) {
      await router.replace({ path: '/portal/password' })
      return
    }
    await router.replace(safePortalRedirect(route.query.redirect))
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.status === 401) {
        errorMessage.value = '工号或密码错误'
      } else if (error.status === 429) {
        errorMessage.value = '登录尝试次数过多，请稍后再试'
      } else if (error.status === 404) {
        errorMessage.value = '员工门户尚未开启，请联系管理员'
      } else if (error.status === 503) {
        errorMessage.value = '认证服务暂时不可用，请稍后再试'
      } else {
        errorMessage.value = error.message
      }
    } else {
      errorMessage.value = '网络异常，请检查连接后重试'
    }
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="portal-login-page">
    <div class="login-card">
      <div class="brand">
        <span class="brand-mark">API</span>
        <span class="brand-name">安全网关 · 员工门户</span>
      </div>

      <div class="heading">
        <span class="eyebrow">EMPLOYEE PORTAL</span>
        <h1>登录员工门户</h1>
        <p>查询个人用量与配额，提交并领取访问 Token。</p>
      </div>

      <el-form label-position="top" :model="form" @submit.prevent="submit">
        <el-form-item label="工号">
          <el-input
            v-model="form.employeeNo"
            data-test="portal-employee-no"
            size="large"
            placeholder="请输入工号"
            autocomplete="username"
            :prefix-icon="User"
          />
        </el-form-item>
        <el-form-item label="密码">
          <el-input
            v-model="form.password"
            data-test="portal-password"
            size="large"
            type="password"
            placeholder="请输入登录密码"
            autocomplete="current-password"
            show-password
            :prefix-icon="Lock"
          />
        </el-form-item>

        <el-alert
          v-if="errorMessage"
          data-test="portal-login-error"
          class="login-error"
          :title="errorMessage"
          type="error"
          :closable="false"
          show-icon
        />

        <el-button
          data-test="portal-submit"
          class="submit"
          type="primary"
          size="large"
          native-type="submit"
          :loading="loading"
        >
          登录
        </el-button>
      </el-form>

      <ul class="hints">
        <li>
          <el-icon><Key /></el-icon>
          <span>Token 由管理员审批后领取，明文只展示一次</span>
        </li>
        <li>
          <el-icon><CircleCheck /></el-icon>
          <span>首次登录需修改管理员发放的初始密码</span>
        </li>
      </ul>

      <p class="foot-note">
        如需开通账号或重置密码，请联系网关管理员。
      </p>
    </div>
  </div>
</template>

<style scoped>
.portal-login-page {
  position: relative;
  display: grid;
  place-items: center;
  min-height: 100vh;
  padding: 32px 20px;
  overflow: hidden;
  background:
    radial-gradient(circle at 15% 12%, rgba(47, 107, 255, 0.14), transparent 34%),
    radial-gradient(circle at 88% 86%, rgba(70, 140, 255, 0.1), transparent 32%),
    #f4f7fb;
}

.portal-login-page::before {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(47, 107, 255, 0.035) 1px, transparent 1px),
    linear-gradient(90deg, rgba(47, 107, 255, 0.035) 1px, transparent 1px);
  background-size: 44px 44px;
  content: '';
  mask-image: radial-gradient(circle at 50% 40%, black, transparent 72%);
  pointer-events: none;
}

.login-card {
  position: relative;
  width: min(100%, 420px);
  padding: 34px 32px 28px;
  border: 1px solid #e8eef7;
  border-radius: 20px;
  background: #fff;
  box-shadow:
    0 1px 2px rgba(23, 32, 51, 0.04),
    0 24px 60px -34px rgba(23, 32, 51, 0.4);
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 28px;
}

.brand-mark {
  display: grid;
  place-items: center;
  width: 38px;
  height: 38px;
  border-radius: 11px;
  background: linear-gradient(135deg, #2f6bff, #5b8cff);
  box-shadow: 0 10px 24px -12px rgba(47, 107, 255, 0.9);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.7px;
}

.brand-name {
  color: #3f4e66;
  font-size: 13px;
  font-weight: 600;
}

.heading {
  margin-bottom: 22px;
}

.eyebrow {
  display: block;
  margin-bottom: 7px;
  color: #9aa7b8;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.14em;
}

.heading h1 {
  margin: 0 0 7px;
  color: #172033;
  font-size: 23px;
  font-weight: 650;
  letter-spacing: -0.01em;
}

.heading p {
  margin: 0;
  color: #8a97a8;
  font-size: 12px;
}

.login-error {
  margin-bottom: 16px;
}

.submit {
  width: 100%;
  height: 44px;
  margin-top: 4px;
  border-radius: 10px;
  font-size: 14px;
  font-weight: 600;
}

.hints {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 22px 0 0;
  padding: 0;
  list-style: none;
}

.hints li {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  color: #7d8b9d;
  font-size: 11px;
  line-height: 1.5;
}

.hints .el-icon {
  flex-shrink: 0;
  margin-top: 1px;
  color: #2f6bff;
  font-size: 13px;
}

.foot-note {
  margin: 20px 0 0;
  color: #9aa7b8;
  font-size: 11px;
  line-height: 1.6;
  text-align: center;
}

@media (max-width: 460px) {
  .portal-login-page {
    padding: 16px 12px;
  }

  .login-card {
    padding: 26px 20px 22px;
  }
}
</style>
