<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Search } from '@element-plus/icons-vue'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  AdminTokenRequest,
  EmployeeQuota,
  PageResponse,
  QuotaPeriod,
  QuotaType,
  TokenRequestStatus,
} from '../api/types'
import { useAuthStore } from '../stores/auth'
import { formatDateTime } from '../utils/datetime'

const auth = useAuthStore()
const canManage = computed(
  () => auth.isSuperAdmin || auth.user?.role === 'operator',
)

const items = ref<AdminTokenRequest[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const loadError = ref('')

const statusFilter = ref<TokenRequestStatus | ''>('')
const searchQuery = ref('')

const approveVisible = ref(false)
const approving = ref(false)
const approveError = ref('')
const approveTarget = ref<AdminTokenRequest | null>(null)
const approveQuota = ref<EmployeeQuota | null>(null)
const loadingQuota = ref(false)
const approveForm = reactive({
  quotaType: 'tokens' as QuotaType,
  quotaPeriod: '' as QuotaPeriod | '',
  quotaTotal: 0,
  rateLimitRPM: 60,
  note: '',
})

const isQuotaApproval = computed(() => approveTarget.value?.kind !== 'token')

const denyVisible = ref(false)
const denying = ref(false)
const denyError = ref('')
const denyTarget = ref<AdminTokenRequest | null>(null)
const denyNote = ref('')

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

function statusLabel(status: TokenRequestStatus): string {
  return statusMeta[status]?.label ?? status
}

function statusType(
  status: TokenRequestStatus,
): 'success' | 'warning' | 'info' | 'danger' {
  return statusMeta[status]?.type ?? 'info'
}

function periodLabel(period: QuotaPeriod | ''): string {
  switch (period) {
    case 'daily':
      return '每日'
    case 'monthly':
      return '每月'
    case 'total':
      return '总量'
    default:
      return '—'
  }
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const params = new URLSearchParams({
      page: String(page.value),
      page_size: String(pageSize.value),
    })
    if (statusFilter.value) params.set('status', statusFilter.value)
    if (searchQuery.value.trim()) params.set('q', searchQuery.value.trim())
    const response = await api.get<PageResponse<AdminTokenRequest>>(
      `/token-requests?${params.toString()}`,
    )
    items.value = response.items
    total.value = response.total
  } catch (error) {
    loadError.value = apiErrorMessage(error, '申请列表加载失败')
  } finally {
    loading.value = false
  }
}

function search(): void {
  page.value = 1
  void load()
}

function openApprove(request: AdminTokenRequest): void {
  approveTarget.value = request
  approveError.value = ''
  approveQuota.value = null
  approveForm.quotaType = 'tokens'
  approveForm.quotaPeriod = request.kind === 'token' ? 'monthly' : ''
  approveForm.quotaTotal = request.requested_total ?? 0
  approveForm.rateLimitRPM = 60
  approveForm.note = ''
  approveVisible.value = true
  if (request.kind !== 'token') void loadApproveQuota(request.employee_id)
}

async function loadApproveQuota(employeeId: string): Promise<void> {
  loadingQuota.value = true
  try {
    approveQuota.value = await api.get<EmployeeQuota>(
      `/employees/${employeeId}/quota`,
    )
  } catch {
    approveQuota.value = null
  } finally {
    loadingQuota.value = false
  }
}

async function submitApprove(): Promise<void> {
  if (!approveTarget.value) return
  approveError.value = ''
  if (
    !Number.isInteger(approveForm.quotaTotal) ||
    approveForm.quotaTotal <= 0
  ) {
    approveError.value = '审批额度必须是正整数'
    return
  }

  if (isQuotaApproval.value) {
    approving.value = true
    try {
      await api.post<AdminTokenRequest>(
        `/token-requests/${approveTarget.value.id}/approve`,
        {
          quota_total: approveForm.quotaTotal,
          quota_period: approveForm.quotaPeriod || undefined,
          note: approveForm.note.trim(),
        },
      )
      ElMessage.success('提额已批准，员工额度立即生效')
      approveVisible.value = false
      await load()
    } catch (error) {
      approveError.value = apiErrorMessage(error, '审批失败')
    } finally {
      approving.value = false
    }
    return
  }

  if (
    !Number.isInteger(approveForm.rateLimitRPM) ||
    approveForm.rateLimitRPM < 1 ||
    approveForm.rateLimitRPM > 100000
  ) {
    approveError.value = '速率限制必须在 1 到 100000 之间'
    return
  }

  approving.value = true
  try {
    await api.post<AdminTokenRequest>(
      `/token-requests/${approveTarget.value.id}/approve`,
      {
        quota_type: approveForm.quotaType,
        quota_period: approveForm.quotaPeriod || 'monthly',
        quota_total: approveForm.quotaTotal,
        rate_limit_rpm: approveForm.rateLimitRPM,
        note: approveForm.note.trim(),
      },
    )
    ElMessage.success('申请已批准，员工可自行领取 Token')
    approveVisible.value = false
    await load()
  } catch (error) {
    approveError.value = apiErrorMessage(error, '审批失败')
  } finally {
    approving.value = false
  }
}

function openDeny(request: AdminTokenRequest): void {
  denyTarget.value = request
  denyError.value = ''
  denyNote.value = ''
  denyVisible.value = true
}

async function submitDeny(): Promise<void> {
  if (!denyTarget.value) return
  denyError.value = ''
  if (!denyNote.value.trim()) {
    denyError.value = '请填写拒绝原因'
    return
  }

  denying.value = true
  try {
    await api.post<AdminTokenRequest>(
      `/token-requests/${denyTarget.value.id}/deny`,
      { note: denyNote.value.trim() },
    )
    ElMessage.success('申请已拒绝')
    denyVisible.value = false
    await load()
  } catch (error) {
    denyError.value = apiErrorMessage(error, '拒绝申请失败')
  } finally {
    denying.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="token-requests-page">
    <section class="page-heading">
      <div>
        <h2>提额与 Token 申请审批</h2>
        <p>
          提额申请批准后立即更新员工的共享额度，原 Key 继续可用；旧版 Token 申请批准后由员工自行领取。
        </p>
      </div>
    </section>

    <el-card class="table-card" shadow="never">
      <div class="toolbar">
        <el-input
          v-model="searchQuery"
          class="search-input"
          clearable
          placeholder="搜索工号或姓名"
          :prefix-icon="Search"
          @keyup.enter="search"
          @clear="search"
        />
        <el-select
          v-model="statusFilter"
          class="status-select"
          clearable
          placeholder="全部状态"
          @change="search"
        >
          <el-option label="待审批" value="pending" />
          <el-option label="已生效" value="granted" />
          <el-option label="待领取" value="approved" />
          <el-option label="已领取" value="claimed" />
          <el-option label="已拒绝" value="denied" />
          <el-option label="已取消" value="cancelled" />
          <el-option label="已过期" value="expired" />
        </el-select>
        <el-button @click="search">查询</el-button>
      </div>

      <el-alert
        v-if="loadError"
        :title="loadError"
        type="error"
        :closable="false"
        show-icon
        class="load-error"
      />

      <el-table v-loading="loading" :data="items" stripe>
        <el-table-column label="员工" min-width="150">
          <template #default="{ row }">
            <div class="employee-name">{{ row.employee_name }}</div>
            <div class="employee-meta">
              {{ row.employee_no }}
              <span v-if="row.department_name"> · {{ row.department_name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="类型" width="80">
          <template #default="{ row }">
            <el-tag
              size="small"
              :type="row.kind === 'token' ? 'info' : 'primary'"
              effect="plain"
            >
              {{ row.kind === 'token' ? 'Token' : '提额' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="reason" label="申请理由" min-width="200" show-overflow-tooltip />
        <el-table-column label="申请额度" width="115">
          <template #default="{ row }">
            <span v-if="row.requested_total">
              {{ numberFormat.format(row.requested_total) }}
            </span>
            <span v-else class="muted">默认</span>
          </template>
        </el-table-column>
        <el-table-column label="审批额度" width="130">
          <template #default="{ row }">
            <template v-if="row.granted_total !== null">
              {{ numberFormat.format(row.granted_total) }}
              <div v-if="row.kind !== 'token' && row.status === 'granted'" class="sub-line">
                已生效
              </div>
              <div v-else-if="row.granted_rpm" class="sub-line">
                {{ row.granted_rpm }} RPM
              </div>
            </template>
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
        <el-table-column label="提交时间" width="145">
          <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="审批说明" min-width="150" show-overflow-tooltip>
          <template #default="{ row }">
            <span v-if="row.review_note">{{ row.review_note }}</span>
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <el-table-column
          v-if="canManage"
          label="操作"
          width="130"
          fixed="right"
        >
          <template #default="{ row }">
            <div v-if="row.status === 'pending'" class="row-actions">
              <el-button link type="primary" @click="openApprove(row)">
                批准
              </el-button>
              <el-button link type="danger" @click="openDeny(row)">
                拒绝
              </el-button>
            </div>
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无申请记录" :image-size="80" />
        </template>
      </el-table>

      <div class="pagination-row">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          layout="total, prev, pager, next"
          background
          @current-change="load"
        />
      </div>
    </el-card>

    <el-dialog
      v-model="approveVisible"
      :title="isQuotaApproval ? '批准提额申请' : '批准 Token 申请'"
      width="540px"
      :close-on-click-modal="false"
    >
      <div v-if="approveTarget" class="target-summary">
        <strong>{{ approveTarget.employee_name }}（{{ approveTarget.employee_no }}）</strong>
        <p>{{ approveTarget.reason }}</p>
      </div>

      <el-form label-position="top" @submit.prevent="submitApprove">
        <template v-if="isQuotaApproval">
          <div v-loading="loadingQuota" class="current-quota">
            <template v-if="approveQuota">
              <div class="current-quota-head">
                <span>当前额度</span>
                <strong>{{ numberFormat.format(approveQuota.quota_total) }}</strong>
              </div>
              <div class="current-quota-meta">
                已用 {{ numberFormat.format(approveQuota.quota_used) }} ·
                {{ periodLabel(approveQuota.quota_period) }} ·
                剩余 {{ numberFormat.format(approveQuota.quota_remaining) }}
              </div>
            </template>
            <span v-else-if="!loadingQuota" class="muted">
              未获取到当前额度，将以本次审批值初始化
            </span>
          </div>

          <el-form-item label="批准后的额度总量" required>
            <el-input-number
              v-model="approveForm.quotaTotal"
              :min="1"
              :controls="false"
              class="full-width"
            />
            <div class="field-hint">
              填写提额后的总额度，而不是增量；批准后对员工的所有 Key 立即生效。
            </div>
          </el-form-item>
          <el-form-item label="生效周期">
            <el-select v-model="approveForm.quotaPeriod" class="full-width">
              <el-option label="保持当前周期" value="" />
              <el-option label="每日" value="daily" />
              <el-option label="每月" value="monthly" />
              <el-option label="总量（不自动重置）" value="total" />
            </el-select>
            <div class="field-hint">
              更改周期会重置已用额度；不修改时保持员工现有周期。
            </div>
          </el-form-item>
        </template>

        <template v-else>
          <div class="form-grid">
            <el-form-item label="额度类型">
              <el-select v-model="approveForm.quotaType">
                <el-option label="Token 数" value="tokens" />
                <el-option label="请求数" value="requests" />
              </el-select>
            </el-form-item>
            <el-form-item label="额度周期">
              <el-select v-model="approveForm.quotaPeriod">
                <el-option label="每日" value="daily" />
                <el-option label="每月" value="monthly" />
                <el-option label="总量" value="total" />
              </el-select>
            </el-form-item>
          </div>
          <div class="form-grid">
            <el-form-item label="审批额度" required>
              <el-input-number
                v-model="approveForm.quotaTotal"
                :min="1"
                :controls="false"
                class="full-width"
              />
            </el-form-item>
            <el-form-item label="速率限制（RPM）" required>
              <el-input-number
                v-model="approveForm.rateLimitRPM"
                :min="1"
                :max="100000"
                :controls="false"
                class="full-width"
              />
            </el-form-item>
          </div>
        </template>

        <el-form-item label="审批说明">
          <el-input
            v-model="approveForm.note"
            type="textarea"
            :rows="2"
            maxlength="500"
            placeholder="可选，员工可在门户中看到"
          />
        </el-form-item>

        <el-alert
          v-if="approveError"
          :title="approveError"
          type="error"
          :closable="false"
          show-icon
        />
      </el-form>

      <template #footer>
        <el-button @click="approveVisible = false">取消</el-button>
        <el-button type="primary" :loading="approving" @click="submitApprove">
          {{ isQuotaApproval ? '批准提额' : '批准申请' }}
        </el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="denyVisible"
      title="拒绝 Token 申请"
      width="480px"
      :close-on-click-modal="false"
    >
      <el-form label-position="top" @submit.prevent="submitDeny">
        <el-form-item label="拒绝原因" required>
          <el-input
            v-model="denyNote"
            type="textarea"
            :rows="3"
            maxlength="500"
            show-word-limit
            placeholder="员工可在门户中看到该说明"
          />
        </el-form-item>
        <el-alert
          v-if="denyError"
          :title="denyError"
          type="error"
          :closable="false"
          show-icon
        />
      </el-form>

      <template #footer>
        <el-button @click="denyVisible = false">取消</el-button>
        <el-button type="danger" :loading="denying" @click="submitDeny">
          拒绝申请
        </el-button>
      </template>
    </el-dialog>
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

.table-card {
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.table-card :deep(.el-card__body) {
  padding: 22px 22px 18px;
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid #f0f4f9;
}

.search-input {
  width: 260px;
}

.status-select {
  width: 150px;
}

.load-error {
  margin-bottom: 16px;
}

.employee-name {
  color: #26384d;
  font-size: 13px;
  font-weight: 600;
}

.employee-meta {
  color: #9aa7b8;
  font-size: 10px;
}

.sub-line {
  color: #9aa7b8;
  font-size: 10px;
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

.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 18px;
}

.muted {
  color: #a8b4c2;
}

.target-summary {
  margin-bottom: 16px;
  padding: 12px 14px;
  border: 1px solid #eaf0f8;
  border-radius: 10px;
  background: #f8faff;
}

.target-summary strong {
  color: #26384d;
  font-size: 13px;
}

.target-summary p {
  margin: 6px 0 0;
  color: var(--gw-text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

.current-quota {
  min-height: 44px;
  margin-bottom: 16px;
  padding: 12px 14px;
  border: 1px solid #eaf0f8;
  border-radius: 10px;
  background: #f8faff;
}

.current-quota-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  color: #7b8a9e;
  font-size: 11px;
  font-weight: 600;
}

.current-quota-head strong {
  color: #26384d;
  font-size: 17px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}

.current-quota-meta {
  margin-top: 4px;
  color: #93a0b2;
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}

.field-hint {
  margin-top: 5px;
  color: #9aa7b8;
  font-size: 11px;
  line-height: 1.5;
}

.full-width {
  width: 100%;
}

@media (max-width: 640px) {
  .search-input,
  .status-select {
    width: 100%;
  }

  .form-grid {
    grid-template-columns: 1fr;
    gap: 0;
  }
}
</style>
