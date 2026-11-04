<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Promotion } from '@element-plus/icons-vue'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  AuditSettings,
  AuditSettingsPayload,
  PortalSettings,
  SMTPTestResult,
  SMTPSettings,
  SMTPSettingsPayload,
} from '../api/types'

interface SMTPForm {
  host: string
  port: number
  username: string
  password: string
  from: string
  to: string
  use_tls: boolean
}

const smtpForm = ref<SMTPForm>({
  host: '',
  port: 587,
  username: '',
  password: '',
  from: '',
  to: '',
  use_tls: true,
})
const loadedSMTP = ref<SMTPSettings | null>(null)
const smtpLoading = ref(false)
const smtpSaving = ref(false)
const smtpTesting = ref(false)
const smtpError = ref('')

const auditForm = ref<AuditSettings>({
  retention_days: 180,
  alert_enabled: true,
})
const auditLoading = ref(false)
const auditSaving = ref(false)
const auditError = ref('')

const portalForm = ref<PortalSettings>({
  enabled: false,
  default_quota: {
    type: 'tokens',
    period: 'monthly',
    total: 1_000_000,
    rpm: 60,
    expires_days: 180,
  },
  max_request_total: 5_000_000,
  request_ttl_days: 14,
  claim_ttl_hours: 72,
  usage_max_days: 90,
  self_service_enabled: true,
  max_active_tokens: 5,
  allow_self_rotate: true,
  allow_self_revoke: true,
  auto_approve_quota: false,
})
const portalLoading = ref(false)
const portalSaving = ref(false)
const portalError = ref('')

function applySMTP(settings: SMTPSettings): void {
  loadedSMTP.value = settings
  smtpForm.value = {
    host: settings.host,
    port: settings.port || 587,
    username: settings.username,
    password: '',
    from: settings.from,
    to: settings.to.join('\n'),
    use_tls: settings.use_tls,
  }
}

async function loadSMTP(): Promise<void> {
  smtpLoading.value = true
  smtpError.value = ''
  try {
    const settings = await api.get<SMTPSettings>('/settings/smtp')
    applySMTP(settings)
  } catch (error) {
    smtpError.value = apiErrorMessage(error, 'SMTP 设置加载失败')
  } finally {
    smtpLoading.value = false
  }
}

function parseRecipients(raw: string): string[] {
  return raw
    .split(/\r?\n|,/)
    .map((value) => value.trim())
    .filter(Boolean)
}

function validateSMTP(): string {
  const host = smtpForm.value.host.trim()
  const from = smtpForm.value.from.trim()
  const username = smtpForm.value.username.trim()
  const recipients = parseRecipients(smtpForm.value.to)
  if (!host) return '请输入 SMTP 服务器地址'
  if (
    !Number.isInteger(smtpForm.value.port) ||
    smtpForm.value.port < 1 ||
    smtpForm.value.port > 65535
  ) {
    return 'SMTP 端口必须在 1 到 65535 之间'
  }
  if (!from) return '请输入发件人地址'
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(from)) return '发件人地址格式不正确'
  if (recipients.length === 0) return '请至少填写一个告警收件人'
  for (const recipient of recipients) {
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(recipient)) {
      return `收件人地址格式不正确：${recipient}`
    }
  }

  const loaded = loadedSMTP.value
  if (!smtpForm.value.password && username) {
    if (!loaded?.password_set) return '请输入 SMTP 密码'
    if (
      host !== loaded.host ||
      username !== loaded.username
    ) {
      return '修改 SMTP 服务器或用户名时必须重新输入密码'
    }
  }
  return ''
}

async function saveSMTP(): Promise<void> {
  smtpError.value = validateSMTP()
  if (smtpError.value) return

  const payload: SMTPSettingsPayload = {
    host: smtpForm.value.host.trim(),
    port: smtpForm.value.port,
    username: smtpForm.value.username.trim(),
    password: smtpForm.value.password,
    from: smtpForm.value.from.trim(),
    to: parseRecipients(smtpForm.value.to),
    use_tls: smtpForm.value.use_tls,
  }

  smtpSaving.value = true
  try {
    const settings = await api.put<SMTPSettings>('/settings/smtp', payload)
    applySMTP(settings)
    ElMessage.success('SMTP 设置已保存')
  } catch (error) {
    smtpError.value = apiErrorMessage(error, 'SMTP 设置保存失败')
  } finally {
    smtpSaving.value = false
  }
}

async function testSMTP(): Promise<void> {
  smtpTesting.value = true
  smtpError.value = ''
  try {
    await api.post<SMTPTestResult>('/settings/smtp/test')
    ElMessage.success('测试邮件已发送，请检查收件箱')
  } catch (error) {
    smtpError.value = apiErrorMessage(error, '测试邮件发送失败')
  } finally {
    smtpTesting.value = false
  }
}

async function loadAuditSettings(): Promise<void> {
  auditLoading.value = true
  auditError.value = ''
  try {
    auditForm.value = await api.get<AuditSettings>('/settings/audit')
  } catch (error) {
    auditError.value = apiErrorMessage(error, '审计设置加载失败')
  } finally {
    auditLoading.value = false
  }
}

async function saveAuditSettings(): Promise<void> {
  auditError.value = ''
  if (
    !Number.isInteger(auditForm.value.retention_days) ||
    auditForm.value.retention_days < 1 ||
    auditForm.value.retention_days > 3650
  ) {
    auditError.value = '审计保留天数必须在 1 到 3650 天之间'
    return
  }

  const payload: AuditSettingsPayload = {
    retention_days: auditForm.value.retention_days,
    alert_enabled: auditForm.value.alert_enabled,
  }
  auditSaving.value = true
  try {
    auditForm.value = await api.put<AuditSettings>(
      '/settings/audit',
      payload,
    )
    ElMessage.success('审计设置已保存')
  } catch (error) {
    auditError.value = apiErrorMessage(error, '审计设置保存失败')
  } finally {
    auditSaving.value = false
  }
}

async function loadPortalSettings(): Promise<void> {
  portalLoading.value = true
  portalError.value = ''
  try {
    portalForm.value = await api.get<PortalSettings>('/settings/portal')
  } catch (error) {
    portalError.value = apiErrorMessage(error, '员工门户设置加载失败')
  } finally {
    portalLoading.value = false
  }
}

function validatePortalSettings(): string {
  const settings = portalForm.value
  if (settings.default_quota.total < 0) {
    return '默认额度不能为负数'
  }
  if (
    !Number.isInteger(settings.default_quota.rpm) ||
    settings.default_quota.rpm < 1 ||
    settings.default_quota.rpm > 100000
  ) {
    return '默认速率限制必须在 1 到 100000 之间'
  }
  if (
    !Number.isInteger(settings.default_quota.expires_days) ||
    settings.default_quota.expires_days < 1 ||
    settings.default_quota.expires_days > 3650
  ) {
    return '默认有效期必须在 1 到 3650 天之间'
  }
  if (
    !Number.isInteger(settings.max_request_total) ||
    settings.max_request_total < 1
  ) {
    return '单次申请上限必须是正整数'
  }
  if (
    !Number.isInteger(settings.request_ttl_days) ||
    settings.request_ttl_days < 1 ||
    settings.request_ttl_days > 90
  ) {
    return '申请有效天数必须在 1 到 90 之间'
  }
  if (
    !Number.isInteger(settings.claim_ttl_hours) ||
    settings.claim_ttl_hours < 1 ||
    settings.claim_ttl_hours > 720
  ) {
    return '领取窗口必须在 1 到 720 小时之间'
  }
  if (
    !Number.isInteger(settings.usage_max_days) ||
    settings.usage_max_days < 1 ||
    settings.usage_max_days > 365
  ) {
    return '用量查询范围必须在 1 到 365 天之间'
  }
  if (
    !Number.isInteger(settings.max_active_tokens) ||
    settings.max_active_tokens < 1 ||
    settings.max_active_tokens > 50
  ) {
    return '有效 Key 上限必须在 1 到 50 之间'
  }
  return ''
}

async function savePortalSettings(): Promise<void> {
  portalError.value = validatePortalSettings()
  if (portalError.value) return

  portalSaving.value = true
  try {
    portalForm.value = await api.put<PortalSettings>(
      '/settings/portal',
      portalForm.value,
    )
    ElMessage.success(
      portalForm.value.enabled ? '员工门户设置已保存并启用' : '员工门户已关闭',
    )
  } catch (error) {
    portalError.value = apiErrorMessage(error, '员工门户设置保存失败')
  } finally {
    portalSaving.value = false
  }
}

onMounted(() => {
  void loadSMTP()
  void loadAuditSettings()
  void loadPortalSettings()
})
</script>

<template>
  <div class="settings-page">
    <section class="page-heading">
      <div>
        <h2>系统设置</h2>
        <p>配置 DLP 告警邮件、审计日志保留策略与员工门户。仅超级管理员可访问。</p>
      </div>
    </section>

    <el-row :gutter="18">
      <el-col :xs="24" :lg="14">
        <el-card v-loading="smtpLoading" class="settings-card" shadow="never">
          <div class="card-heading">
            <div>
              <h3>SMTP 邮件告警</h3>
              <p>DLP 命中且规则开启告警时，系统会向收件人发送通知。</p>
            </div>
            <el-tag
              v-if="loadedSMTP?.password_set"
              size="small"
              type="success"
              effect="plain"
            >
              密码已保存
            </el-tag>
          </div>

          <el-form label-position="top" @submit.prevent="saveSMTP">
            <div class="form-grid">
              <el-form-item label="SMTP 服务器" required>
                <el-input
                  v-model="smtpForm.host"
                  data-test="smtp-host"
                  maxlength="255"
                  placeholder="smtp.example.com"
                />
              </el-form-item>
              <el-form-item label="端口" required>
                <el-input-number
                  v-model="smtpForm.port"
                  :min="1"
                  :max="65535"
                  :controls="false"
                  class="full-width"
                />
              </el-form-item>
            </div>

            <div class="form-grid">
              <el-form-item label="用户名">
                <el-input
                  v-model="smtpForm.username"
                  maxlength="255"
                  autocomplete="off"
                  placeholder="可选，留空表示不认证"
                />
              </el-form-item>
              <el-form-item label="密码（留空表示不修改）">
                <el-input
                  v-model="smtpForm.password"
                  data-test="smtp-password"
                  type="password"
                  show-password
                  autocomplete="new-password"
                  maxlength="1024"
                  placeholder="不修改请留空"
                />
              </el-form-item>
            </div>

            <el-form-item label="发件人地址" required>
              <el-input
                v-model="smtpForm.from"
                maxlength="255"
                placeholder="gateway@example.com"
              />
            </el-form-item>

            <el-form-item label="告警收件人" required>
              <el-input
                v-model="smtpForm.to"
                type="textarea"
                :rows="3"
                maxlength="2000"
                placeholder="每行一个邮箱地址"
              />
            </el-form-item>

            <el-form-item label="连接加密">
              <div class="switch-field">
                <el-switch
                  v-model="smtpForm.use_tls"
                  active-text="使用 TLS"
                  inactive-text="明文"
                />
                <div class="field-hint">
                  建议始终启用 TLS。修改服务器或用户名后需要重新输入密码。
                </div>
              </div>
            </el-form-item>

            <el-alert
              v-if="smtpError"
              :title="smtpError"
              type="error"
              :closable="false"
              show-icon
              class="form-alert"
            />

            <div class="form-actions">
              <el-button
                data-test="save-smtp"
                type="primary"
                :loading="smtpSaving"
                @click="saveSMTP"
              >
                保存设置
              </el-button>
              <el-button
                :icon="Promotion"
                :loading="smtpTesting"
                @click="testSMTP"
              >
                发送测试邮件
              </el-button>
            </div>
          </el-form>
        </el-card>
      </el-col>

      <el-col :xs="24" :lg="10">
        <el-card v-loading="auditLoading" class="settings-card" shadow="never">
          <div class="card-heading">
            <div>
              <h3>审计与告警策略</h3>
              <p>控制审计日志保留时长与 DLP 命中告警开关。</p>
            </div>
          </div>

          <el-form label-position="top" @submit.prevent="saveAuditSettings">
            <el-form-item label="审计日志保留天数" required>
              <el-input-number
                v-model="auditForm.retention_days"
                :min="1"
                :max="3650"
                :controls="false"
                class="full-width"
              />
              <div class="field-hint">
                超过保留期的审计日志会由维护任务定期清理，最长 3650 天。
              </div>
            </el-form-item>

            <el-form-item label="DLP 命中邮件告警">
              <el-switch
                v-model="auditForm.alert_enabled"
                active-text="开启"
                inactive-text="关闭"
              />
              <div class="field-hint">
                关闭后仍记录审计日志，但不再按规则发送告警邮件。
              </div>
            </el-form-item>

            <el-alert
              v-if="auditError"
              :title="auditError"
              type="error"
              :closable="false"
              show-icon
              class="form-alert"
            />

            <div class="form-actions">
              <el-button
                data-test="save-audit-settings"
                type="primary"
                :loading="auditSaving"
                @click="saveAuditSettings"
              >
                保存设置
              </el-button>
            </div>
          </el-form>
        </el-card>
      </el-col>
    </el-row>

    <el-card v-loading="portalLoading" class="settings-card portal-card" shadow="never">
      <div class="card-heading">
        <div>
          <h3>员工门户</h3>
          <p>
            控制员工自助门户、员工默认共享额度、自助 Key 与提额审批策略。关闭门户后员工将无法登录。
          </p>
        </div>
        <el-tag :type="portalForm.enabled ? 'success' : 'info'" size="small" effect="plain">
          {{ portalForm.enabled ? '已启用' : '已关闭' }}
        </el-tag>
      </div>

      <el-form label-position="top" @submit.prevent="savePortalSettings">
        <el-form-item label="启用员工门户">
          <div class="switch-field">
            <el-switch
              v-model="portalForm.enabled"
              active-text="启用"
              inactive-text="关闭"
            />
            <div class="field-hint">
              关闭后门户 API 返回 404，已登录员工的会话也不再可用。
            </div>
          </div>
        </el-form-item>

        <div class="form-grid form-grid-3">
          <el-form-item label="默认额度类型">
            <el-select v-model="portalForm.default_quota.type">
              <el-option label="Token 数" value="tokens" />
              <el-option label="请求数" value="requests" />
            </el-select>
          </el-form-item>
          <el-form-item label="默认额度周期">
            <el-select v-model="portalForm.default_quota.period">
              <el-option label="每日" value="daily" />
              <el-option label="每月" value="monthly" />
              <el-option label="总量" value="total" />
            </el-select>
          </el-form-item>
          <el-form-item label="默认额度值">
            <el-input-number
              v-model="portalForm.default_quota.total"
              :min="0"
              :controls="false"
              class="full-width"
            />
          </el-form-item>
        </div>

        <div class="form-grid form-grid-3">
          <el-form-item label="默认速率限制（RPM）">
            <el-input-number
              v-model="portalForm.default_quota.rpm"
              :min="1"
              :max="100000"
              :controls="false"
              class="full-width"
            />
          </el-form-item>
          <el-form-item label="默认有效期（天）">
            <el-input-number
              v-model="portalForm.default_quota.expires_days"
              :min="1"
              :max="3650"
              :controls="false"
              class="full-width"
            />
          </el-form-item>
          <el-form-item label="单次申请额度上限">
            <el-input-number
              v-model="portalForm.max_request_total"
              :min="1"
              :controls="false"
              class="full-width"
            />
          </el-form-item>
        </div>

        <div class="form-grid form-grid-3">
          <el-form-item label="申请有效天数">
            <el-input-number
              v-model="portalForm.request_ttl_days"
              :min="1"
              :max="90"
              :controls="false"
              class="full-width"
            />
            <div class="field-hint">审批前申请单的有效期。</div>
          </el-form-item>
          <el-form-item label="领取窗口（小时）">
            <el-input-number
              v-model="portalForm.claim_ttl_hours"
              :min="1"
              :max="720"
              :controls="false"
              class="full-width"
            />
            <div class="field-hint">批准后员工必须在此时间内领取。</div>
          </el-form-item>
          <el-form-item label="用量查询最长天数">
            <el-input-number
              v-model="portalForm.usage_max_days"
              :min="1"
              :max="365"
              :controls="false"
              class="full-width"
            />
            <div class="field-hint">员工可查询的最长历史区间。</div>
          </el-form-item>
        </div>

        <h4 class="sub-heading">自助 Key 与提额</h4>
        <div class="form-grid form-grid-3">
          <el-form-item label="允许员工自助创建 Key">
            <div class="switch-field">
              <el-switch
                v-model="portalForm.self_service_enabled"
                active-text="允许"
                inactive-text="禁止"
              />
              <div class="field-hint">
                关闭后员工只能使用管理员分配的 Key，也无法提交提额申请。
              </div>
            </div>
          </el-form-item>
          <el-form-item label="每位员工有效 Key 上限">
            <el-input-number
              v-model="portalForm.max_active_tokens"
              :min="1"
              :max="50"
              :controls="false"
              class="full-width"
            />
            <div class="field-hint">仅统计启用且未过期的 Key。</div>
          </el-form-item>
          <el-form-item label="提额自动批准">
            <div class="switch-field">
              <el-switch
                v-model="portalForm.auto_approve_quota"
                active-text="自动批准"
                inactive-text="人工审批"
              />
              <div class="field-hint">
                开启后提额申请立即生效；仍受「单次申请额度上限」约束。
              </div>
            </div>
          </el-form-item>
        </div>

        <div class="form-grid form-grid-3">
          <el-form-item label="允许自助轮换 Key">
            <el-switch
              v-model="portalForm.allow_self_rotate"
              active-text="允许"
              inactive-text="禁止"
            />
          </el-form-item>
          <el-form-item label="允许自助撤销 Key">
            <el-switch
              v-model="portalForm.allow_self_revoke"
              active-text="允许"
              inactive-text="禁止"
            />
          </el-form-item>
        </div>

        <el-alert
          v-if="portalError"
          :title="portalError"
          type="error"
          :closable="false"
          show-icon
          class="form-alert"
        />

        <div class="form-actions">
          <el-button
            data-test="save-portal-settings"
            type="primary"
            :loading="portalSaving"
            @click="savePortalSettings"
          >
            保存设置
          </el-button>
        </div>
      </el-form>
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

.settings-card {
  margin-bottom: 18px;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.settings-card :deep(.el-card__body) {
  padding: 22px;
}

.card-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 18px;
}

.card-heading h3 {
  margin: 0 0 6px;
  color: var(--gw-text);
  font-size: 15px;
  font-weight: 600;
}

.card-heading p {
  margin: 0;
  color: var(--gw-text-muted);
  font-size: 11px;
  line-height: 1.6;
}

.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

.form-grid-3 {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.sub-heading {
  margin: 22px 0 12px;
  padding-top: 18px;
  border-top: 1px solid #eef3f9;
  color: #31445e;
  font-size: 13px;
  font-weight: 650;
}

.full-width {
  width: 100%;
}

.switch-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.switch-field .field-hint {
  margin-top: 0;
}

.field-hint {
  margin-top: 6px;
  color: var(--gw-text-muted);
  font-size: 11px;
  line-height: 1.5;
}

.form-alert {
  margin-bottom: 16px;
}

.form-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

@media (max-width: 960px) {
  .form-grid-3 {
    grid-template-columns: 1fr 1fr;
  }
}

@media (max-width: 640px) {
  .form-grid,
  .form-grid-3 {
    grid-template-columns: 1fr;
    gap: 0;
  }
}
</style>
