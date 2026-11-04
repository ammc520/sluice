<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CopyDocument, Plus, Refresh } from '@element-plus/icons-vue'

import { ApiError, portalApi } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  PortalQuota,
  PortalToken,
  PortalTokenCreateResult,
  PortalTokenRotateResult,
} from '../api/types'
import { formatDateTime } from '../utils/datetime'

const numberFormat = new Intl.NumberFormat('zh-CN')

const quotas = ref<PortalQuota | null>(null)
const tokens = ref<PortalToken[]>([])
const loading = ref(false)
const loadError = ref('')

const createVisible = ref(false)
const creating = ref(false)
const createError = ref('')
const form = reactive({
  name: '',
  quotaTotal: undefined as number | undefined,
  rateLimitRPM: undefined as number | undefined,
})

const secretVisible = ref(false)
const secret = ref('')
const secretTitle = ref('')
const secretItem = ref<PortalToken | null>(null)
const rotatingId = ref('')
const revokingId = ref('')

const canCreate = computed(
  () =>
    !!quotas.value?.self_service_enabled &&
    (quotas.value?.active_tokens ?? 0) < (quotas.value?.max_active_tokens ?? 0),
)

const createDisabledReason = computed(() => {
  if (!quotas.value) return ''
  if (!quotas.value.self_service_enabled) return '管理员未开启自助创建 Key'
  if ((quotas.value.active_tokens ?? 0) >= (quotas.value.max_active_tokens ?? 0)) {
    return `已达到 ${quotas.value.max_active_tokens} 个有效 Key 的上限`
  }
  return ''
})

const quotaTypeLabel = computed(() =>
  quotas.value?.quota_type === 'requests' ? '请求数' : 'Tokens',
)

const quotaPeriodLabel = computed(() => {
  switch (quotas.value?.quota_period) {
    case 'daily':
      return '每日额度'
    case 'monthly':
      return '每月额度'
    default:
      return '总额度'
  }
})

const usagePercent = computed(() => {
  const quota = quotas.value
  if (!quota || quota.quota_total <= 0) return 0
  return Math.min(100, Math.round((quota.quota_used / quota.quota_total) * 100))
})

const progressStatus = computed<'' | 'warning' | 'exception'>(() => {
  if (usagePercent.value >= 100) return 'exception'
  if (usagePercent.value >= 80) return 'warning'
  return ''
})

const resetText = computed(() => {
  const quota = quotas.value
  if (!quota) return '—'
  if (quota.quota_period === 'total') return '不自动重置'
  return quota.resets_at ? formatDateTime(quota.resets_at) : '—'
})

function percentOf(used: number, total: number): number {
  if (!total) return 0
  return Math.min(100, Math.round((used / total) * 100))
}

function tokenQuotaStatus(used: number, total: number): '' | 'warning' | 'exception' {
  const pct = percentOf(used, total)
  if (pct >= 100) return 'exception'
  if (pct >= 80) return 'warning'
  return ''
}

function tokenCapText(token: PortalToken): string {
  if (token.quota_total === null) return '不限'
  return numberFormat.format(token.quota_total)
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const [quota, list] = await Promise.all([
      portalApi.get<PortalQuota>('/me/quota'),
      portalApi.get<{ items: PortalToken[] }>('/me/tokens'),
    ])
    quotas.value = quota
    tokens.value = list.items
  } catch (error) {
    loadError.value = apiErrorMessage(error, 'Key 列表加载失败')
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  createError.value = ''
  form.name = ''
  form.quotaTotal = undefined
  form.rateLimitRPM = undefined
  createVisible.value = true
}

async function submitCreate(): Promise<void> {
  createError.value = ''
  const name = form.name.trim()
  if (name.length < 2 || name.length > 40) {
    createError.value = 'Key 名称需为 2 到 40 个字符'
    return
  }
  if (
    form.quotaTotal !== undefined &&
    (!Number.isInteger(form.quotaTotal) || form.quotaTotal < 0)
  ) {
    createError.value = '单 Key 上限必须是非负整数'
    return
  }
  if (form.rateLimitRPM !== undefined && !Number.isInteger(form.rateLimitRPM)) {
    createError.value = '速率限制必须是整数'
    return
  }

  creating.value = true
  try {
    const result = await portalApi.post<PortalTokenCreateResult>('/me/tokens', {
      name,
      quota_total: form.quotaTotal ?? null,
      rate_limit_rpm: form.rateLimitRPM,
    })
    createVisible.value = false
    showSecret('Key 已创建', result.token, result.item)
    await load()
  } catch (error) {
    if (error instanceof ApiError && error.code === 'token_limit_reached') {
      createError.value = '已达到有效 Key 数量上限，请先撤销不用的 Key'
    } else if (error instanceof ApiError && error.code === 'self_service_disabled') {
      createError.value = '管理员已关闭自助创建 Key'
    } else {
      createError.value = apiErrorMessage(error, 'Key 创建失败')
    }
  } finally {
    creating.value = false
  }
}

function showSecret(title: string, plain: string, item: PortalToken): void {
  secretTitle.value = title
  secret.value = plain
  secretItem.value = item
  secretVisible.value = true
}

async function rotate(token: PortalToken): Promise<void> {
  try {
    await ElMessageBox.confirm(
      '轮换后旧 Key 立即失效，使用中的客户端需要换成新 Key。新 Key 明文只展示一次。',
      '确认轮换 Key',
      {
        type: 'warning',
        confirmButtonText: '轮换',
        cancelButtonText: '取消',
      },
    )
  } catch {
    return
  }
  rotatingId.value = token.id
  try {
    const result = await portalApi.post<PortalTokenRotateResult>(
      `/me/tokens/${token.id}/rotate`,
    )
    showSecret('Key 已轮换', result.token, result.item)
    await load()
  } catch (error) {
    if (error instanceof ApiError && error.code === 'self_rotate_disabled') {
      ElMessage.error('管理员已关闭自助轮换')
    } else if (error instanceof ApiError && error.code === 'token_expired') {
      ElMessage.error('该 Key 已过期，无法轮换')
    } else if (error instanceof ApiError && error.code === 'token_revoked') {
      ElMessage.error('该 Key 已撤销，无法轮换')
    } else {
      ElMessage.error(apiErrorMessage(error, 'Key 轮换失败'))
    }
  } finally {
    rotatingId.value = ''
  }
}

async function revoke(token: PortalToken): Promise<void> {
  try {
    await ElMessageBox.confirm(
      '撤销后该 Key 立即失效，所有使用它的客户端将无法继续调用。',
      '确认撤销 Key',
      {
        type: 'warning',
        confirmButtonText: '撤销',
        cancelButtonText: '取消',
      },
    )
  } catch {
    return
  }
  revokingId.value = token.id
  try {
    await portalApi.post(`/me/tokens/${token.id}/revoke`)
    ElMessage.success('Key 已撤销')
    await load()
  } catch (error) {
    if (error instanceof ApiError && error.code === 'self_revoke_disabled') {
      ElMessage.error('管理员已关闭自助撤销')
    } else {
      ElMessage.error(apiErrorMessage(error, 'Key 撤销失败'))
    }
  } finally {
    revokingId.value = ''
  }
}

async function copySecret(): Promise<void> {
  if (!secret.value) return
  try {
    await navigator.clipboard.writeText(secret.value)
    ElMessage.success('Key 已复制到剪贴板')
  } catch {
    ElMessage.warning('复制失败，请手动选择并复制')
  }
}

function closeSecret(): void {
  secretVisible.value = false
  secret.value = ''
  secretItem.value = null
}

onMounted(load)
</script>

<template>
  <div class="tokens-page">
    <section class="page-heading">
      <div>
        <h2>我的 Key</h2>
        <p>同一员工的多个 Key 共享个人额度，Key 只是调用凭据，不单独分配额度。</p>
      </div>
      <div class="heading-actions">
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button
          type="primary"
          :icon="Plus"
          :disabled="!canCreate"
          data-test="portal-create-token"
          @click="openCreate"
        >
          新建 Key
        </el-button>
      </div>
    </section>

    <el-alert
      v-if="loadError"
      :title="loadError"
      type="error"
      :closable="false"
      show-icon
      class="load-error"
    />

    <el-card v-if="quotas" class="quota-card" shadow="never">
      <div class="quota-main">
        <div class="quota-heading">
          <div>
            <span class="quota-eyebrow">个人额度 · {{ quotaPeriodLabel }}</span>
            <div class="quota-numbers">
              <strong>{{ numberFormat.format(quotas.quota_remaining) }}</strong>
              <span>剩余 {{ quotaTypeLabel }}</span>
            </div>
          </div>
          <el-tag
            :type="usagePercent >= 100 ? 'danger' : usagePercent >= 80 ? 'warning' : 'success'"
            size="small"
            effect="light"
          >
            已用 {{ usagePercent }}%
          </el-tag>
        </div>
        <el-progress
          :percentage="usagePercent"
          :status="progressStatus"
          :stroke-width="8"
          :show-text="false"
        />
        <div class="quota-meta">
          <span>
            已用 {{ numberFormat.format(quotas.quota_used) }} /
            {{ numberFormat.format(quotas.quota_total) }}
            {{ quotaTypeLabel }}
          </span>
          <span>下次重置：{{ resetText }}</span>
        </div>
      </div>
      <div class="quota-side">
        <div class="side-stat">
          <span class="side-label">有效 Key</span>
          <strong>{{ quotas.active_tokens }} / {{ quotas.max_active_tokens }}</strong>
        </div>
        <el-button
          v-if="quotas.can_request_increase"
          text
          type="primary"
          class="request-link"
          @click="$router.push('/portal/requests')"
        >
          额度不足？申请提额
        </el-button>
      </div>
    </el-card>

    <el-alert
      v-if="createDisabledReason"
      :title="createDisabledReason"
      type="info"
      :closable="false"
      show-icon
      class="hint"
    />

    <el-card class="table-card" shadow="never">
      <el-table v-loading="loading" :data="tokens" stripe>
        <el-table-column label="Key" min-width="210">
          <template #default="{ row }">
            <div class="key-cell">
              <strong class="key-name">{{ row.name || '未命名 Key' }}</strong>
              <code class="token-prefix">{{ row.token_prefix }}…{{ row.token_last4 }}</code>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="单 Key 上限" min-width="170">
          <template #default="{ row }">
            <div class="cap-head">
              <span class="muted">仅限制此 Key</span>
              <span class="cap-numbers">
                {{ numberFormat.format(row.quota_used) }} / {{ tokenCapText(row) }}
              </span>
            </div>
            <el-progress
              v-if="row.quota_total"
              :percentage="percentOf(row.quota_used, row.quota_total)"
              :status="tokenQuotaStatus(row.quota_used, row.quota_total)"
              :stroke-width="5"
              :show-text="false"
            />
          </template>
        </el-table-column>
        <el-table-column label="速率限制" width="100">
          <template #default="{ row }">{{ row.rate_limit_rpm }} RPM</template>
        </el-table-column>
        <el-table-column label="状态" width="85">
          <template #default="{ row }">
            <el-tag :type="row.status === 1 ? 'success' : 'info'" size="small">
              {{ row.status === 1 ? '正常' : '已停用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="有效期至" width="130">
          <template #default="{ row }">{{ formatDateTime(row.expires_at) }}</template>
        </el-table-column>
        <el-table-column label="最近使用" width="130">
          <template #default="{ row }">{{ formatDateTime(row.last_used_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="130" fixed="right">
          <template #default="{ row }">
            <div class="row-actions">
              <el-button
                v-if="row.status === 1 && quotas?.allow_self_rotate"
                link
                type="primary"
                :loading="rotatingId === row.id"
                @click="rotate(row)"
              >
                轮换
              </el-button>
              <el-button
                v-if="row.status === 1 && quotas?.allow_self_revoke"
                link
                type="danger"
                :loading="revokingId === row.id"
                @click="revoke(row)"
              >
                撤销
              </el-button>
              <span
                v-if="
                  row.status !== 1 ||
                  (!quotas?.allow_self_rotate && !quotas?.allow_self_revoke)
                "
                class="muted"
              >
                —
              </span>
            </div>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty
            description="暂无 Key，点击右上角「新建 Key」创建"
            :image-size="72"
          />
        </template>
      </el-table>
    </el-card>

    <p class="foot-note">
      Key 明文只在创建或轮换时展示一次。请勿在代码仓库、聊天工具或日志中保存明文 Key。
    </p>

    <el-dialog
      v-model="createVisible"
      title="新建 Key"
      width="520px"
      :close-on-click-modal="false"
    >
      <el-form label-position="top" @submit.prevent="submitCreate">
        <el-form-item label="Key 名称" required>
          <el-input
            v-model="form.name"
            maxlength="40"
            show-word-limit
            placeholder="例如：本地开发、生产服务（2-40 字）"
          />
        </el-form-item>
        <el-form-item label="单 Key 上限（可选）">
          <el-input-number
            v-model="form.quotaTotal"
            :min="0"
            :controls="false"
            class="full-width"
            placeholder="留空则不限制单 Key 用量"
          />
          <div class="field-hint">
            只限制这个 Key 自己的累计用量，不改变员工共享额度；员工额度始终按所有 Key 合计扣减。
          </div>
        </el-form-item>
        <el-form-item label="速率限制（RPM，可选）">
          <el-input-number
            v-model="form.rateLimitRPM"
            :min="1"
            :max="100000"
            :controls="false"
            class="full-width"
            placeholder="留空则使用管理员默认值"
          />
        </el-form-item>

        <el-alert
          v-if="createError"
          :title="createError"
          type="error"
          :closable="false"
          show-icon
        />
      </el-form>

      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button
          type="primary"
          :loading="creating"
          data-test="portal-submit-token"
          @click="submitCreate"
        >
          创建
        </el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="secretVisible"
      :title="secretTitle"
      width="560px"
      :close-on-click-modal="false"
      @closed="closeSecret"
    >
      <div class="secret-panel">
        <el-alert
          type="warning"
          :closable="false"
          show-icon
          title="请立即复制并妥善保存"
          description="Key 明文只展示这一次。关闭窗口后无法再次查看，遗失请轮换或重建。"
        />

        <code class="secret-value">{{ secret }}</code>

        <el-button type="primary" :icon="CopyDocument" @click="copySecret">
          复制 Key
        </el-button>

        <dl v-if="secretItem" class="secret-meta">
          <div>
            <dt>名称</dt>
            <dd>{{ secretItem.name || '未命名 Key' }}</dd>
          </div>
          <div>
            <dt>前缀</dt>
            <dd>{{ secretItem.token_prefix }}…{{ secretItem.token_last4 }}</dd>
          </div>
          <div>
            <dt>单 Key 上限</dt>
            <dd>{{ tokenCapText(secretItem) }}</dd>
          </div>
          <div>
            <dt>速率限制</dt>
            <dd>{{ secretItem.rate_limit_rpm }} RPM</dd>
          </div>
        </dl>
      </div>

      <template #footer>
        <el-button type="primary" @click="closeSecret">我已保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
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

.heading-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.heading-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}

.load-error,
.hint {
  margin-bottom: 16px;
}

.quota-card {
  margin-bottom: 16px;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.quota-card :deep(.el-card__body) {
  display: flex;
  align-items: stretch;
  gap: 26px;
  padding: 20px 22px;
}

.quota-main {
  flex: 1;
  min-width: 0;
}

.quota-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
  margin-bottom: 10px;
}

.quota-eyebrow {
  color: #7b8a9e;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
}

.quota-numbers {
  display: flex;
  align-items: baseline;
  gap: 7px;
  margin-top: 4px;
}

.quota-numbers strong {
  color: var(--gw-text);
  font-size: 27px;
  font-weight: 650;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
  line-height: 1.1;
}

.quota-numbers span {
  color: #93a0b2;
  font-size: 11px;
}

.quota-meta {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 8px 16px;
  margin-top: 9px;
  color: #93a0b2;
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}

.quota-side {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  justify-content: space-between;
  gap: 10px;
  flex-shrink: 0;
  padding-left: 24px;
  border-left: 1px solid var(--gw-border);
}

.side-stat {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 3px;
}

.side-label {
  color: #93a0b2;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}

.side-stat strong {
  color: #26384d;
  font-size: 19px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}

.request-link {
  padding: 0;
  font-size: 12px;
}

.table-card {
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.table-card :deep(.el-card__body) {
  padding: 18px 20px;
}

.key-cell {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.key-name {
  color: #26384d;
  font-size: 13px;
  font-weight: 600;
}

.token-prefix {
  color: #52657c;
  font-family: ui-monospace, Consolas, monospace;
  font-size: 11px;
  white-space: nowrap;
}

.cap-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 5px;
  font-size: 10px;
}

.cap-numbers {
  color: #26384d;
  font-size: 11px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.row-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  white-space: nowrap;
}

.row-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}

.muted {
  color: #a8b4c2;
}

.foot-note {
  margin: 16px 2px 0;
  color: #9aa7b8;
  font-size: 11px;
  line-height: 1.6;
}

.full-width {
  width: 100%;
}

.field-hint {
  margin-top: 5px;
  color: #9aa7b8;
  font-size: 11px;
  line-height: 1.5;
}

.secret-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.secret-value {
  display: block;
  padding: 14px 16px;
  border: 1px dashed #c9d8f2;
  border-radius: 10px;
  background: #f7faff;
  color: #1f3b63;
  font-family: ui-monospace, Consolas, monospace;
  font-size: 12px;
  line-height: 1.7;
  overflow-wrap: anywhere;
}

.secret-meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 18px;
  margin: 4px 0 0;
}

.secret-meta dt {
  color: #9aa7b8;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}

.secret-meta dd {
  margin: 3px 0 0;
  color: #26384d;
  font-size: 12px;
}

@media (max-width: 760px) {
  .page-heading {
    flex-direction: column;
  }

  .quota-card :deep(.el-card__body) {
    flex-direction: column;
    gap: 16px;
  }

  .quota-side {
    flex-direction: row;
    align-items: center;
    padding-top: 14px;
    padding-left: 0;
    border-top: 1px solid var(--gw-border);
    border-left: none;
  }

  .side-stat {
    align-items: flex-start;
  }

  .secret-meta {
    grid-template-columns: 1fr;
  }
}
</style>
