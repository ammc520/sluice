<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  Employee,
  PageResponse,
  Token,
  TokenCreateResult,
  TokenPayload,
} from '../api/types'
import TokenCreateDialog from '../components/TokenCreateDialog.vue'
import { useAuthStore } from '../stores/auth'
import { formatDateTime } from '../utils/datetime'

const auth = useAuthStore()
const canManage = computed(
  () => auth.isSuperAdmin || auth.user?.role === 'operator',
)

const employees = ref<Employee[]>([])
const tokens = ref<Token[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const loadError = ref('')

const employeeFilter = ref('')
const statusFilter = ref<number | ''>('')

const dialogVisible = ref(false)
const dialogTitle = ref('新增 Token')
const editingToken = ref<Token | null>(null)
const createdToken = ref<string | null>(null)
const dialogError = ref('')
const saving = ref(false)

const employeeMap = computed(
  () => new Map(employees.value.map((employee) => [employee.id, employee])),
)

const numberFormat = new Intl.NumberFormat('zh-CN')

function employeeLabel(id: string): string {
  const employee = employeeMap.value.get(id)
  if (!employee) return id.slice(0, 8)
  return `${employee.name}（${employee.employee_no}）`
}

function quotaTypeLabel(type: Token['quota_type']): string {
  return type === 'tokens' ? 'Token 数' : '请求数'
}

function quotaPeriodLabel(period: Token['quota_period']): string {
  if (period === 'daily') return '每日'
  if (period === 'monthly') return '每月'
  return '总量'
}

function quotaUsedText(token: Token): string {
  const totalText =
    token.quota_total === null ? '不限' : numberFormat.format(token.quota_total)
  return `${numberFormat.format(token.quota_used)} / ${totalText}`
}

async function loadEmployees(): Promise<void> {
  try {
    const response = await api.get<PageResponse<Employee>>(
      '/employees?page=1&page_size=200',
    )
    employees.value = response.items
  } catch (error) {
    ElMessage.error(apiErrorMessage(error, '员工列表加载失败'))
  }
}

async function loadTokens(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const params = new URLSearchParams({
      page: String(page.value),
      page_size: String(pageSize.value),
    })
    if (employeeFilter.value) params.set('employee_id', employeeFilter.value)
    if (statusFilter.value !== '') {
      params.set('status', String(statusFilter.value))
    }
    const response = await api.get<PageResponse<Token>>(
      `/tokens?${params.toString()}`,
    )
    tokens.value = response.items
    total.value = response.total
  } catch (error) {
    loadError.value = apiErrorMessage(error, 'Token 列表加载失败')
  } finally {
    loading.value = false
  }
}

function search(): void {
  page.value = 1
  void loadTokens()
}

function resetFilters(): void {
  employeeFilter.value = ''
  statusFilter.value = ''
  page.value = 1
  void loadTokens()
}

function openCreate(): void {
  editingToken.value = null
  createdToken.value = null
  dialogError.value = ''
  dialogTitle.value = '新增 Token'
  dialogVisible.value = true
}

function openEdit(token: Token): void {
  editingToken.value = token
  createdToken.value = null
  dialogError.value = ''
  dialogTitle.value = '编辑 Token'
  dialogVisible.value = true
}

function resetDialog(): void {
  editingToken.value = null
  createdToken.value = null
  dialogError.value = ''
  dialogTitle.value = '新增 Token'
}

async function saveToken(payload: TokenPayload): Promise<void> {
  dialogError.value = ''
  saving.value = true
  try {
    if (editingToken.value) {
      await api.put<Token>(`/tokens/${editingToken.value.id}`, payload)
      dialogVisible.value = false
      ElMessage.success('Token 设置已更新')
      await loadTokens()
      return
    }

    const result = await api.post<TokenCreateResult>('/tokens', payload)
    createdToken.value = result.token
    dialogTitle.value = 'Token 已创建'
    await loadTokens()
  } catch (error) {
    dialogError.value = apiErrorMessage(error)
  } finally {
    saving.value = false
  }
}

async function revokeToken(token: Token): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `撤销后，使用 ${token.token_prefix}…${token.token_last4} 的请求将立即失败，且无法恢复。`,
      '确认撤销 Token',
      {
        type: 'warning',
        confirmButtonText: '确认撤销',
        cancelButtonText: '取消',
      },
    )
  } catch {
    return
  }

  try {
    await api.post<Token>(`/tokens/${token.id}/revoke`)
    ElMessage.success('Token 已撤销')
    await loadTokens()
  } catch (error) {
    ElMessage.error(apiErrorMessage(error, '撤销 Token 失败'))
  }
}

async function rotateToken(token: Token): Promise<void> {
  try {
    await ElMessageBox.confirm(
      '轮换会立即撤销旧 Token 并生成一个新的 Token，使用旧 Token 的客户端需要更新配置。',
      '确认轮换 Token',
      {
        type: 'warning',
        confirmButtonText: '确认轮换',
        cancelButtonText: '取消',
      },
    )
  } catch {
    return
  }

  try {
    const result = await api.post<TokenCreateResult>(
      `/tokens/${token.id}/rotate`,
    )
    editingToken.value = null
    createdToken.value = result.token
    dialogError.value = ''
    dialogTitle.value = 'Token 已轮换'
    dialogVisible.value = true
    await loadTokens()
  } catch (error) {
    ElMessage.error(apiErrorMessage(error, '轮换 Token 失败'))
  }
}

onMounted(() => {
  void loadEmployees()
  void loadTokens()
})
</script>

<template>
  <div class="tokens-page">
    <section class="page-heading">
      <div>
        <h2>Token 管理</h2>
        <p>签发员工访问 Token，并配置额度、速率、有效期与来源 IP 限制。</p>
      </div>
      <el-button
        v-if="canManage"
        data-test="create-token"
        type="primary"
        :icon="Plus"
        @click="openCreate"
      >
        新增 Token
      </el-button>
    </section>

    <el-card class="table-card" shadow="never">
      <div class="toolbar">
        <el-select
          v-model="employeeFilter"
          class="employee-select"
          clearable
          filterable
          placeholder="全部员工"
          @change="search"
        >
          <el-option
            v-for="employee in employees"
            :key="employee.id"
            :label="`${employee.name}（${employee.employee_no}）`"
            :value="employee.id"
          />
        </el-select>
        <el-select
          v-model="statusFilter"
          class="status-select"
          clearable
          placeholder="全部状态"
          @change="search"
        >
          <el-option label="正常" :value="1" />
          <el-option label="已撤销" :value="0" />
        </el-select>
        <el-button @click="resetFilters">重置</el-button>
      </div>

      <el-alert
        v-if="loadError"
        :title="loadError"
        type="error"
        :closable="false"
        show-icon
        class="load-error"
      />

      <el-table v-loading="loading" :data="tokens" stripe>
        <el-table-column label="员工" min-width="170">
          <template #default="{ row }">
            <div class="employee-cell">{{ employeeLabel(row.employee_id) }}</div>
          </template>
        </el-table-column>
        <el-table-column label="Token" min-width="180">
          <template #default="{ row }">
            <code class="token-prefix">{{ row.token_prefix }}…{{ row.token_last4 }}</code>
          </template>
        </el-table-column>
        <el-table-column label="额度" width="140">
          <template #default="{ row }">
            {{ quotaPeriodLabel(row.quota_period) }} · {{ quotaTypeLabel(row.quota_type) }}
          </template>
        </el-table-column>
        <el-table-column label="已用 / 总量" width="150">
          <template #default="{ row }">{{ quotaUsedText(row) }}</template>
        </el-table-column>
        <el-table-column prop="rate_limit_rpm" label="RPM" width="90" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.status === 1 ? 'success' : 'info'" size="small">
              {{ row.status === 1 ? '正常' : '已撤销' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="过期时间" width="160">
          <template #default="{ row }">{{ formatDateTime(row.expires_at) }}</template>
        </el-table-column>
        <el-table-column label="最后使用" width="160">
          <template #default="{ row }">{{ formatDateTime(row.last_used_at) }}</template>
        </el-table-column>
        <el-table-column
          v-if="canManage"
          label="操作"
          width="160"
          fixed="right"
        >
          <template #default="{ row }">
            <template v-if="row.status === 1">
              <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
              <el-button link type="warning" @click="rotateToken(row)">轮换</el-button>
              <el-button link type="danger" @click="revokeToken(row)">撤销</el-button>
            </template>
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无 Token" :image-size="80" />
        </template>
      </el-table>

      <div class="pagination-row">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          layout="total, prev, pager, next"
          background
          @current-change="loadTokens"
        />
      </div>
    </el-card>

    <TokenCreateDialog
      v-model="dialogVisible"
      :employees="employees"
      :initial="editingToken"
      :token="createdToken"
      :saving="saving"
      :error-message="dialogError"
      :title="dialogTitle"
      @submit="saveToken"
      @closed="resetDialog"
    />
  </div>
</template>

<style scoped>
.page-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 22px;
}

.page-heading h2 {
  margin: 0 0 8px;
  color: #1f2d3d;
  font-size: 22px;
}

.page-heading p {
  margin: 0;
  color: #8393a7;
  font-size: 14px;
}

.table-card {
  border: 1px solid #e8edf4;
  border-radius: 12px;
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 18px;
}

.employee-select {
  width: 260px;
}

.status-select {
  width: 150px;
}

.load-error {
  margin-bottom: 16px;
}

.employee-cell {
  color: #26384d;
  font-weight: 500;
}

.token-prefix {
  color: #52657c;
  font-family: ui-monospace, Consolas, monospace;
  font-size: 12px;
}

.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 18px;
}

.muted {
  color: #a8b4c2;
}

@media (max-width: 640px) {
  .page-heading {
    flex-direction: column;
  }

  .employee-select,
  .status-select {
    width: 100%;
  }
}
</style>
