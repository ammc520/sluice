<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Lock, User } from '@element-plus/icons-vue'

import { ApiError } from '../api/client'
import { safeRedirect } from '../router/redirect'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const form = reactive({ username: '', password: '' })
const loading = ref(false)
const errorMessage = ref('')

async function submit(): Promise<void> {
  errorMessage.value = ''
  const username = form.username.trim()
  if (!username || !form.password) {
    errorMessage.value = '请输入用户名和密码'
    return
  }

  loading.value = true
  try {
    await auth.login(username, form.password)
    await router.replace(safeRedirect(route.query.redirect))
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.status === 401) {
        errorMessage.value = '用户名或密码错误'
      } else if (error.status === 429) {
        errorMessage.value = '登录尝试次数过多，请稍后再试'
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
  <div class="login-page">
    <div class="login-shell">
      <section class="login-intro">
        <div class="intro-mark">API</div>
        <h1>企业 API 安全网关</h1>
        <p>统一访问入口 · 精细配额管理 · 敏感数据防护</p>
        <ul>
          <li>集中托管上游模型凭据</li>
          <li>按员工与周期控制用量</li>
          <li>DLP 实时阻断与审计留痕</li>
        </ul>
      </section>

      <el-card class="login-card" shadow="never">
        <div class="card-heading">
          <h2>管理员登录</h2>
          <p>请使用独立管理员账号访问控制台</p>
        </div>

        <el-form :model="form" @submit.prevent="submit">
          <el-form-item>
            <el-input
              v-model="form.username"
              data-test="username"
              size="large"
              placeholder="管理员账号"
              autocomplete="username"
              :prefix-icon="User"
            />
          </el-form-item>
          <el-form-item>
            <el-input
              v-model="form.password"
              data-test="password"
              size="large"
              type="password"
              placeholder="登录密码"
              autocomplete="current-password"
              show-password
              :prefix-icon="Lock"
            />
          </el-form-item>

          <el-alert
            v-if="errorMessage"
            data-test="login-error"
            class="login-error"
            :title="errorMessage"
            type="error"
            :closable="false"
            show-icon
          />

          <el-button
            data-test="submit"
            class="login-submit"
            type="primary"
            size="large"
            native-type="submit"
            :loading="loading"
          >
            登录
          </el-button>
        </el-form>

        <p class="security-note">仅限授权管理员访问，所有操作均会记录审计日志</p>
      </el-card>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  display: grid;
  place-items: center;
  min-height: 100vh;
  padding: 32px 20px;
  background:
    radial-gradient(circle at 15% 20%, rgba(64, 158, 255, 0.22), transparent 32%),
    radial-gradient(circle at 85% 80%, rgba(44, 112, 190, 0.18), transparent 30%),
    #0f1a26;
}

.login-shell {
  display: grid;
  grid-template-columns: minmax(280px, 1fr) minmax(340px, 420px);
  gap: 72px;
  align-items: center;
  width: min(100%, 980px);
}

.login-intro {
  color: #fff;
}

.intro-mark {
  display: grid;
  place-items: center;
  width: 52px;
  height: 52px;
  margin-bottom: 28px;
  border-radius: 14px;
  background: linear-gradient(135deg, #409eff, #2f7fd4);
  font-size: 17px;
  font-weight: 700;
  letter-spacing: 0.8px;
}

.login-intro h1 {
  margin: 0 0 16px;
  font-size: 36px;
  line-height: 1.2;
  letter-spacing: 0.5px;
}

.login-intro p {
  margin: 0 0 32px;
  color: #a9b9ca;
  font-size: 15px;
}

.login-intro ul {
  margin: 0;
  padding: 0;
  list-style: none;
  color: #c6d2df;
  font-size: 14px;
  line-height: 2.2;
}

.login-intro li::before {
  content: '';
  display: inline-block;
  width: 6px;
  height: 6px;
  margin-right: 10px;
  border-radius: 50%;
  background: #409eff;
  vertical-align: middle;
}

.login-card {
  border: none;
  border-radius: 16px;
  box-shadow: 0 24px 70px rgba(4, 12, 24, 0.45);
}

.card-heading {
  margin-bottom: 26px;
}

.card-heading h2 {
  margin: 0 0 8px;
  color: #1f2d3d;
  font-size: 24px;
}

.card-heading p {
  margin: 0;
  color: #8393a7;
  font-size: 13px;
}

.login-error {
  margin-bottom: 18px;
}

.login-submit {
  width: 100%;
  margin-top: 4px;
}

.security-note {
  margin: 24px 0 0;
  color: #98a6b8;
  font-size: 12px;
  line-height: 1.6;
  text-align: center;
}

@media (max-width: 820px) {
  .login-shell {
    grid-template-columns: 1fr;
    gap: 36px;
  }

  .login-intro {
    text-align: center;
  }

  .intro-mark {
    margin: 0 auto 20px;
  }

  .login-intro h1 {
    font-size: 28px;
  }

  .login-intro ul {
    display: none;
  }
}
</style>
