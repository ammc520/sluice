<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Lock } from '@element-plus/icons-vue'

import { ApiError } from '../api/client'
import { usePortalAuthStore } from '../stores/portalAuth'

const portal = usePortalAuthStore()
const router = useRouter()

const form = reactive({
  currentPassword: '',
  newPassword: '',
  confirmPassword: '',
})
const saving = ref(false)
const errorMessage = ref('')

const forced = computed(() => portal.mustChangePassword)
const employeeNo = computed(() => portal.employee?.employee_no ?? '')

async function submit(): Promise<void> {
  errorMessage.value = ''
  if (!form.currentPassword) {
    errorMessage.value = '请输入当前密码'
    return
  }
  if (form.newPassword.length < 10) {
    errorMessage.value = '新密码至少需要 10 个字符'
    return
  }
  if (form.newPassword.length > 128) {
    errorMessage.value = '新密码不能超过 128 个字符'
    return
  }
  if (form.newPassword === form.currentPassword) {
    errorMessage.value = '新密码不能与当前密码相同'
    return
  }
  if (employeeNo.value && form.newPassword.toLowerCase() === employeeNo.value.toLowerCase()) {
    errorMessage.value = '新密码不能与工号相同'
    return
  }
  if (form.newPassword !== form.confirmPassword) {
    errorMessage.value = '两次输入的新密码不一致'
    return
  }

  saving.value = true
  try {
    await portal.changePassword(form.currentPassword, form.newPassword)
    ElMessage.success('密码已修改，请使用新密码重新登录')
    await router.replace({ path: '/portal/login' })
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.code === 'invalid_current_password') {
        errorMessage.value = '当前密码不正确'
      } else if (error.code === 'csrf_failed') {
        errorMessage.value = '页面会话已失效，请重新登录'
      } else {
        errorMessage.value = error.message
      }
    } else {
      errorMessage.value = '网络异常，请稍后重试'
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="password-page">
    <el-card class="password-card" shadow="never">
      <div class="heading">
        <span class="eyebrow">
          {{ forced ? 'FIRST SIGN-IN' : 'ACCOUNT SECURITY' }}
        </span>
        <h2>{{ forced ? '首次登录，请修改初始密码' : '修改登录密码' }}</h2>
        <p v-if="forced">
          管理员发放的初始密码仅限首次登录使用。修改成功后需要使用新密码重新登录。
        </p>
        <p v-else>修改成功后，其他设备上的登录状态也会同时失效。</p>
      </div>

      <el-form label-position="top" @submit.prevent="submit">
        <el-form-item label="当前密码" required>
          <el-input
            v-model="form.currentPassword"
            data-test="portal-current-password"
            size="large"
            type="password"
            autocomplete="current-password"
            show-password
            :prefix-icon="Lock"
          />
        </el-form-item>
        <el-form-item label="新密码" required>
          <el-input
            v-model="form.newPassword"
            data-test="portal-new-password"
            size="large"
            type="password"
            autocomplete="new-password"
            show-password
            :prefix-icon="Lock"
          />
          <div class="field-hint">
            至少 10 个字符，不能与当前密码或工号相同。
          </div>
        </el-form-item>
        <el-form-item label="确认新密码" required>
          <el-input
            v-model="form.confirmPassword"
            data-test="portal-confirm-password"
            size="large"
            type="password"
            autocomplete="new-password"
            show-password
            :prefix-icon="Lock"
          />
        </el-form-item>

        <el-alert
          v-if="errorMessage"
          :title="errorMessage"
          type="error"
          :closable="false"
          show-icon
          class="form-alert"
        />

        <el-button
          data-test="portal-save-password"
          type="primary"
          size="large"
          class="submit"
          native-type="submit"
          :loading="saving"
        >
          修改密码并重新登录
        </el-button>
      </el-form>

      <el-button
        v-if="!forced"
        text
        class="back-button"
        @click="router.push('/portal/profile')"
      >
        返回个人信息
      </el-button>
    </el-card>
  </div>
</template>

<style scoped>
.password-page {
  display: flex;
  justify-content: center;
  padding-top: 12px;
}

.password-card {
  width: min(100%, 520px);
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.password-card :deep(.el-card__body) {
  padding: 30px 30px 24px;
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
  letter-spacing: 0.12em;
}

.heading h2 {
  margin: 0 0 7px;
  color: var(--gw-text);
  font-size: 21px;
  font-weight: 650;
}

.heading p {
  margin: 0;
  color: var(--gw-text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.field-hint {
  margin-top: 5px;
  color: #9aa7b8;
  font-size: 11px;
  line-height: 1.5;
}

.form-alert {
  margin-bottom: 14px;
}

.submit {
  width: 100%;
  height: 44px;
  border-radius: 10px;
  font-weight: 600;
}

.back-button {
  display: block;
  margin: 14px auto 0;
  color: var(--gw-text-muted);
}
</style>
