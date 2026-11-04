<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CopyDocument, Plus } from '@element-plus/icons-vue'

import { ApiError, portalApi } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  PortalClaimResult,
  PortalQuota,
  PortalTokenRequest,
  PortalTokenRequestListResponse,
  TokenRequestStatus,
} from '../api/types'
import { formatDateTime } from '../utils/datetime'

const requests = ref<PortalTokenRequest[]>([])
const quota = ref<PortalQuota | null>(null)
const loading = ref(false)
const loadError = ref('')

const createVisible = ref(false)
const creating = ref(false)
const createError = ref('')
const form = reactive({ reason: '', requestedTotal: undefined as number | undefined })

const claimResult = ref<PortalClaimResult | null>(null)
const claimVisible = ref(false)
const claimingId = ref('')

const numberFormat = new Intl.NumberFormat('zh-CN')

const statusMeta: Record<
  TokenRequestStatus,
  { label: string; type: 'success' | 'warning' | 'info' | 'danger' }
> = {
  pending: { label: '待审批', type: 'warning' },
  approved: { label: '待领取', type: 'success' },
  granted: { label: '已生效', type: 'success' },
  denied: { label: '已拒绝', type: 'danger' },
  cancelled: { label: '已取消', type: 'info' },
  claimed: { label: '已领取', type: 'info' },
  expired: { label: '已过期', type: 'info' },
}

const activeRequest = computed(() =>
  requests.value.find((request) => request.status === 'pending'),
)

const canCreate = computed(() => !activeRequest.value && !!quota.value)

const quotaTypeLabel = computed(() =>
  quota.value?.quota_type === 'requests' ? '请求数' : 'Tokens',
)

const quotaPeriodLabel = computed(() => {
  switch (quota.value?.quota_period) {
    case 'daily':
      return '每日'
    case 'monthly':
      return '每月'
    default:
      return '总'
  }
})

function statusLabel(status: TokenRequestStatus): string {
  return statusMeta[status]?.label ?? status
}

function statusType(
  status: TokenRequestStatus,
): 'success' | 'warning' | 'info' | 'danger' {
  return statusMeta[status]?.type ?? 'info'
}

function isQuotaRequest(request: PortalTokenRequest): boolean {
  return request.kind !== 'token'
}

function grantedText(request: PortalTokenRequest): string {
  if (request.granted_total === null) return '—'
  const type = request.granted_quota_type === 'requests' ? '请求' : 'Token'
  return `${numberFormat.format(request.granted_total)} ${type}`
}

function requestKindLabel(request: PortalTokenRequest): string {
  return isQuotaRequest(request) ? '提额' : 'Token'
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const [list, current] = await Promise.all([
      portalApi.get<PortalTokenRequestListResponse>('/me/token-requests'),
      portalApi.get<PortalQuota>('/me/quota'),
    ])
    requests.value = list.items
    quota.value = current
  } catch (error) {
    loadError.value = apiErrorMessage(error, '申请记录加载失败')
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  createError.value = ''
  form.reason = ''
  form.requestedTotal = undefined
  createVisible.value = true
}

async function submitCreate(): Promise<void> {
  createError.value = ''
  const reason = form.reason.trim()
  if (reason.length < 10 || reason.length > 500) {
    createError.value = '申请理由需为 10 到 500 个字符'
    return
  }
  if (form.requestedTotal === undefined) {
    createError.value = '请填写申请后的额度总量'
    return
  }
  if (!Number.isInteger(form.requestedTotal) || form.requestedTotal <= 0) {
    createError.value = '申请额度必须是正整数'
    return
  }
  if (quota.value && form.requestedTotal <= quota.value.quota_total) {
    createError.value = `申请额度需大于当前额度 ${numberFormat.format(quota.value.quota_total)}`
    return
  }

  creating.value = true
  try {
    const created = await portalApi.post<PortalTokenRequest>('/me/token-requests', {
      reason,
      requested_total: form.requestedTotal,
    })
    if (created.status === 'granted') {
      ElMessage.success('提额申请已自动通过，额度立即生效')
    } else {
      ElMessage.success('申请已提交，请等待管理员审批')
    }
    createVisible.value = false
    await load()
  } catch (error) {
    if (error instanceof ApiError && error.code === 'active_request_exists') {
      createError.value = '已有一条进行中的申请，请先等待审批或取消后再提交'
    } else {
      createError.value = apiErrorMessage(error, '申请提交失败')
    }
  } finally {
    creating.value = false
  }
}

async function cancelRequest(request: PortalTokenRequest): Promise<void> {
  try {
    await ElMessageBox.confirm('取消后需要重新提交申请。', '确认取消申请', {
      type: 'warning',
      confirmButtonText: '确认取消',
      cancelButtonText: '保留申请',
    })
  } catch {
    return
  }

  try {
    await portalApi.post(`/me/token-requests/${request.id}/cancel`)
    ElMessage.success('申请已取消')
    await load()
  } catch (error) {
    ElMessage.error(apiErrorMessage(error, '取消申请失败'))
  }
}

async function claim(request: PortalTokenRequest): Promise<void> {
  try {
    await ElMessageBox.confirm(
      '领取后 Token 明文只会展示一次，关闭后无法再次查看。请准备好在客户端中保存。',
      '确认领取 Token',
      {
        type: 'warning',
        confirmButtonText: '领取 Token',
        cancelButtonText: '稍后领取',
      },
    )
  } catch {
    return
  }

  claimingId.value = request.id
  try {
    const result = await portalApi.post<PortalClaimResult>(
      `/me/token-requests/${request.id}/claim`,
    )
    claimResult.value = result
    claimVisible.value = true
    await load()
  } catch (error) {
    if (error instanceof ApiError && error.code === 'claim_expired') {
      ElMessage.error('领取窗口已过期，请联系管理员重新审批')
      await load()
    } else {
      ElMessage.error(apiErrorMessage(error, '领取 Token 失败'))
    }
  } finally {
    claimingId.value = ''
  }
}

async function copyToken(): Promise<void> {
  if (!claimResult.value) return
  try {
    await navigator.clipboard.writeText(claimResult.value.token)
    ElMessage.success('Token 已复制到剪贴板')
  } catch {
    ElMessage.warning('复制失败，请手动选择并复制')
  }
}

function closeClaim(): void {
  claimVisible.value = false
  claimResult.value = null
}

onMounted(load)
</script>

<template>
  <div class="requests-page">
    <section class="page-heading">
      <div>
        <h2>额度提额</h2>
        <p>
          所有 Key 共享你的个人额度。额度不足时提交提额申请，批准后立即生效，原 Key 可继续使用。
        </p>
      </div>
      <el-button
        type="primary"
        :icon="Plus"
        :disabled="!canCreate"
        data-test="portal-create-request"
        @click="openCreate"
      >
        申请提额
      </el-button>
    </section>

    <el-alert
      v-if="loadError"
      :title="loadError"
      type="error"
      :closable="false"
      show-icon
      class="load-error"
    />

    <el-card v-if="quota" class="quota-card" shadow="never">
      <div class="quota-item">
        <span class="quota-label">当前{{ quotaPeriodLabel }}额度</span>
        <strong>{{ numberFormat.format(quota.quota_total) }} {{ quotaTypeLabel }}</strong>
      </div>
      <div class="quota-item">
        <span class="quota-label">已使用</span>
        <strong>{{ numberFormat.format(quota.quota_used) }} {{ quotaTypeLabel }}</strong>
      </div>
      <div class="quota-item">
        <span class="quota-label">剩余</span>
        <strong class="remaining">{{ numberFormat.format(quota.quota_remaining) }} {{ quotaTypeLabel }}</strong>
      </div>
      <div class="quota-item">
        <span class="quota-label">下次重置</span>
        <strong>
          {{ quota.quota_period === 'total' ? '不自动重置' : formatDateTime(quota.resets_at) }}
        </strong>
      </div>
    </el-card>

    <el-alert
      v-if="activeRequest"
      class="active-hint"
      type="info"
      :closable="false"
      show-icon
      title="你已有一条待审批的提额申请，审批通过后额度立即生效。"
    />

    <el-card class="table-card" shadow="never">
      <el-table v-loading="loading" :data="requests" stripe>
        <el-table-column label="提交时间" width="150">
          <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="类型" width="80">
          <template #default="{ row }">
            <el-tag size="small" :type="isQuotaRequest(row) ? 'primary' : 'info'" effect="plain">
              {{ requestKindLabel(row) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="reason" label="申请理由" min-width="180" show-overflow-tooltip />
        <el-table-column label="申请额度" width="115">
          <template #default="{ row }">
            <span v-if="row.requested_total">
              {{ numberFormat.format(row.requested_total) }}
            </span>
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <el-table-column label="审批额度" width="125">
          <template #default="{ row }">
            <div v-if="row.granted_total !== null">
              {{ grantedText(row) }}
              <div v-if="row.granted_rpm" class="sub-line">
                {{ row.granted_rpm }} RPM
              </div>
            </div>
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="95">
          <template #default="{ row }">
            <el-tag :type="statusType(row.status)" size="small">
              {{ statusLabel(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="有效期 / 说明" min-width="180">
          <template #default="{ row }">
            <div v-if="row.status === 'approved'">
              领取截止 {{ formatDateTime(row.expires_at) }}
            </div>
            <div v-else-if="row.status === 'granted'">
              {{ isQuotaRequest(row) ? '额度已累加到个人账户' : '已生效' }}
            </div>
            <div v-else-if="row.review_note">{{ row.review_note }}</div>
            <div v-else-if="row.status === 'pending'">
              申请有效期至 {{ formatDateTime(row.expires_at) }}
            </div>
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="110" fixed="right">
          <template #default="{ row }">
            <div class="row-actions">
              <el-button
                v-if="row.can_claim"
                link
                type="primary"
                :loading="claimingId === row.id"
                @click="claim(row)"
              >
                领取
              </el-button>
              <el-button
                v-if="row.can_cancel"
                link
                type="danger"
                @click="cancelRequest(row)"
              >
                取消
              </el-button>
              <span v-if="!row.can_claim && !row.can_cancel" class="muted">—</span>
            </div>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无申请记录" :image-size="72" />
        </template>
      </el-table>
    </el-card>

    <el-dialog
      v-model="createVisible"
      title="申请提升个人额度"
      width="520px"
      :close-on-click-modal="false"
    >
      <el-form label-position="top" @submit.prevent="submitCreate">
        <el-form-item label="申请理由" required>
          <el-input
            v-model="form.reason"
            type="textarea"
            :rows="4"
            maxlength="500"
            show-word-limit
            placeholder="请说明用途、使用场景或所属项目（10-500 字）"
          />
        </el-form-item>
        <el-form-item label="申请后的额度总量" required>
          <el-input-number
            v-model="form.requestedTotal"
            :min="1"
            :controls="false"
            class="full-width"
            :placeholder="
              quota
                ? `当前为 ${numberFormat.format(quota.quota_total)}，请填写提升后的总量`
                : '请输入提升后的额度总量'
            "
          />
          <div class="field-hint">
            填写提额后的额度总量，而不是增量。审批通过后立即生效，无需领取新 Key。
          </div>
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
          data-test="portal-submit-request"
          @click="submitCreate"
        >
          提交申请
        </el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="claimVisible"
      title="Token 已生成"
      width="560px"
      :close-on-click-modal="false"
      @closed="closeClaim"
    >
      <div v-if="claimResult" class="claim-panel">
        <el-alert
          type="warning"
          :closable="false"
          show-icon
          title="请立即复制并妥善保存"
          description="Token 明文只展示这一次。关闭窗口后无法再次查看，遗失需轮换。"
        />

        <code class="claimed-token">{{ claimResult.token }}</code>

        <el-button type="primary" :icon="CopyDocument" @click="copyToken">
          复制 Token
        </el-button>

        <dl class="claim-meta">
          <div>
            <dt>前缀</dt>
            <dd>{{ claimResult.token_prefix }}…{{ claimResult.token_last4 }}</dd>
          </div>
          <div>
            <dt>额度</dt>
            <dd>
              {{ numberFormat.format(claimResult.request.granted_total ?? 0) }}
              {{ claimResult.request.granted_quota_type === 'requests' ? '请求' : 'Tokens' }}
            </dd>
          </div>
          <div>
            <dt>速率限制</dt>
            <dd>{{ claimResult.request.granted_rpm ?? '—' }} RPM</dd>
          </div>
          <div>
            <dt>有效期至</dt>
            <dd>{{ formatDateTime(claimResult.request.granted_expires_at) }}</dd>
          </div>
        </dl>
      </div>

      <template #footer>
        <el-button type="primary" @click="closeClaim">我已保存</el-button>
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
  line-height: 1.6;
}

.load-error,
.active-hint {
  margin-bottom: 16px;
}

.quota-card {
  margin-bottom: 16px;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.quota-card :deep(.el-card__body) {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 18px;
  padding: 18px 22px;
}

.quota-item {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.quota-label {
  color: #93a0b2;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}

.quota-item strong {
  color: #26384d;
  font-size: 16px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}

.quota-item strong.remaining {
  color: var(--gw-primary);
}

.table-card {
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.table-card :deep(.el-card__body) {
  padding: 18px 20px;
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

.sub-line {
  color: #9aa7b8;
  font-size: 10px;
}

.muted {
  color: #a8b4c2;
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

.claim-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.claimed-token {
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

.claim-meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 18px;
  margin: 4px 0 0;
}

.claim-meta dt {
  color: #9aa7b8;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}

.claim-meta dd {
  margin: 3px 0 0;
  color: #26384d;
  font-size: 12px;
}

@media (max-width: 760px) {
  .page-heading {
    flex-direction: column;
  }

  .quota-card :deep(.el-card__body) {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .claim-meta {
    grid-template-columns: 1fr;
  }
}
</style>
