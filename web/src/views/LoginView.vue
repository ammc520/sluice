<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { CircleCheck, Lock, User } from '@element-plus/icons-vue'

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
        <div class="intro-brand">
          <span class="intro-mark">API</span>
          <span class="intro-brand-name">安全网关 · DLP 控制台</span>
        </div>

        <span class="intro-badge">企业级访问控制</span>
        <h1>让每一次模型调用<br />都可管、可控、可追溯</h1>
        <p class="intro-lead">
          统一托管上游凭据，按员工控制配额与速率，实时识别并阻断敏感信息外发。
        </p>

        <ul class="intro-features">
          <li>
            <el-icon><CircleCheck /></el-icon>
            <span>集中托管上游模型密钥，员工无需持有真实 Key</span>
          </li>
          <li>
            <el-icon><CircleCheck /></el-icon>
            <span>按员工、周期与来源 IP 精细限制调用额度</span>
          </li>
          <li>
            <el-icon><CircleCheck /></el-icon>
            <span>DLP 实时阻断、脱敏与完整审计留痕</span>
          </li>
        </ul>

        <div class="intro-foot">
          <span class="pulse-dot" />
          仅限授权管理员访问
        </div>
      </section>

      <section class="login-panel">
        <el-card class="login-card" shadow="never">
          <div class="card-heading">
            <span class="card-eyebrow">ADMIN CONSOLE</span>
            <h2>管理员登录</h2>
            <p>请使用独立管理员账号访问控制台</p>
          </div>

          <el-form label-position="top" :model="form" @submit.prevent="submit">
            <el-form-item label="管理员账号">
              <el-input
                v-model="form.username"
                data-test="username"
                size="large"
                placeholder="请输入管理员账号"
                autocomplete="username"
                :prefix-icon="User"
              />
            </el-form-item>
            <el-form-item label="登录密码">
              <el-input
                v-model="form.password"
                data-test="password"
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
              登录控制台
            </el-button>
          </el-form>

          <p class="security-note">
            仅限授权管理员访问，所有操作均会记录审计日志
          </p>
        </el-card>
      </section>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  position: relative;
  display: grid;
  place-items: center;
  min-height: 100vh;
  padding: 36px 24px;
  overflow: hidden;
  background:
    radial-gradient(circle at 12% 12%, rgba(47, 107, 255, 0.14), transparent 34%),
    radial-gradient(circle at 88% 84%, rgba(70, 140, 255, 0.1), transparent 32%),
    #f4f7fb;
}

.login-page::before {
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

.login-shell {
  position: relative;
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(360px, 430px);
  gap: 56px;
  align-items: center;
  width: min(100%, 1040px);
  padding: 52px;
  border: 1px solid rgba(255, 255, 255, 0.9);
  border-radius: 26px;
  background: rgba(255, 255, 255, 0.72);
  box-shadow:
    0 1px 2px rgba(23, 32, 51, 0.04),
    0 30px 80px -32px rgba(23, 32, 51, 0.28);
  backdrop-filter: blur(18px);
}

.login-intro {
  padding: 8px 0;
}

.intro-brand {
  display: flex;
  align-items: center;
  gap: 11px;
  margin-bottom: 34px;
}

.intro-mark {
  display: grid;
  place-items: center;
  width: 42px;
  height: 42px;
  border-radius: 12px;
  background: linear-gradient(135deg, #2f6bff, #5b8cff);
  box-shadow: 0 10px 24px -12px rgba(47, 107, 255, 0.9);
  color: #fff;
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.7px;
}

.intro-brand-name {
  color: #3f4e66;
  font-size: 13px;
  font-weight: 600;
  letter-spacing: 0.2px;
}

.intro-badge {
  display: inline-flex;
  align-items: center;
  padding: 5px 10px;
  border: 1px solid #dbe6ff;
  border-radius: 999px;
  background: #f1f5ff;
  color: #2f6bff;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.06em;
}

.login-intro h1 {
  margin: 18px 0 16px;
  color: #16233a;
  font-size: 33px;
  font-weight: 650;
  letter-spacing: -0.02em;
  line-height: 1.35;
}

.intro-lead {
  max-width: 460px;
  margin: 0 0 30px;
  color: #61708a;
  font-size: 14px;
  line-height: 1.75;
}

.intro-features {
  display: flex;
  flex-direction: column;
  gap: 13px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.intro-features li {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  color: #4a5a73;
  font-size: 13px;
  line-height: 1.55;
}

.intro-features .el-icon {
  flex-shrink: 0;
  margin-top: 1px;
  color: var(--gw-success);
  font-size: 16px;
}

.intro-foot {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 38px;
  color: #8a97a8;
  font-size: 11px;
}

.pulse-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #1aa06d;
  box-shadow: 0 0 0 4px rgba(26, 160, 109, 0.12);
}

.login-panel {
  display: flex;
  justify-content: flex-end;
}

.login-card {
  width: 100%;
  border: 1px solid #e8eef7;
  border-radius: 20px;
  background: #ffffff;
  box-shadow:
    0 1px 2px rgba(23, 32, 51, 0.04),
    0 24px 60px -34px rgba(23, 32, 51, 0.4);
}

.login-card :deep(.el-card__body) {
  padding: 34px 32px 28px;
}

.card-heading {
  margin-bottom: 24px;
}

.card-eyebrow {
  display: block;
  margin-bottom: 8px;
  color: #9aa7b8;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.14em;
}

.card-heading h2 {
  margin: 0 0 7px;
  color: #172033;
  font-size: 23px;
  font-weight: 650;
  letter-spacing: -0.01em;
}

.card-heading p {
  margin: 0;
  color: #8a97a8;
  font-size: 12px;
}

.login-error {
  margin-bottom: 16px;
}

.login-submit {
  width: 100%;
  margin-top: 4px;
  height: 44px;
  border-radius: 10px;
  font-size: 14px;
  font-weight: 600;
}

.security-note {
  margin: 22px 0 0;
  color: #9aa7b8;
  font-size: 11px;
  line-height: 1.6;
  text-align: center;
}

@media (max-width: 900px) {
  .login-shell {
    grid-template-columns: 1fr;
    gap: 30px;
    padding: 34px 22px;
    border-radius: 20px;
  }

  .login-intro {
    text-align: center;
  }

  .intro-brand {
    justify-content: center;
    margin-bottom: 22px;
  }

  .intro-badge {
    margin: 0 auto;
  }

  .login-intro h1 {
    font-size: 26px;
  }

  .intro-lead {
    margin-right: auto;
    margin-left: auto;
  }

  .intro-features,
  .intro-foot {
    display: none;
  }

  .login-panel {
    justify-content: center;
  }
}

@media (max-width: 520px) {
  .login-page {
    padding: 16px 12px;
  }

  .login-shell {
    padding: 24px 14px;
  }

  .login-card :deep(.el-card__body) {
    padding: 26px 20px 22px;
  }
}
</style>
