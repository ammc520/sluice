<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh, Search } from '@element-plus/icons-vue'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  AuditListResponse,
  AuditLog,
  AuditRevealResult,
  Employee,
  PageResponse,
} from '../api/types'
import { useAuthStore } from '../stores/auth'
import { formatDateTime, shanghaiDateDaysAgo } from '../utils/datetime'

type DLPActionFilter = '' | 'none' | 'block' | 'mask' | 'audit'

const auth = useAuthStore()
const canReveal = computed(() => auth.isSuperAdmin)

const employees = ref<Employee[]>([])
const employeeMap = computed(
  () => new Map(employees.value.map((employee) => [employee.id, employee])),
)

const dateRange = ref<[string, string] | null>([
  shanghaiDateDaysAgo(6),
  shanghaiDateDaysAgo(0),
])
const employeeFilter = ref('')
const statusCode = ref<number | undefined>(undefined)
const dlpAction = ref<DLPActionFilter>('')
const modelFilter = ref('')

const items = ref<AuditLog[]>([])
const nextCursor = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const loadError = ref('')

const detailVisible = ref(false)
const detailLoading = ref(false)
const detailRecord = ref<AuditLog | null>(null)
const detailError = ref('')

const revealVisible = ref(false)
const revealRecord = ref<AuditLog | null>(null)
const revealReason = ref('')
const revealLoading = ref(false)
const revealError = ref('')
const revealedPrompt = ref('')
const revealedResponse = ref('')
const hasRevealed = ref(false)

const revealReasonLength = computed(
  () => Array.from(revealReason.value.trim()).length,
)

const actionLabels: Record<string, string> = {
  block: '阻断',
  mask: '脱敏',
  audit: '仅审计',
  none: '无',
}

const actionTagTypes: Record<string, 'danger' | 'warning' | 'info'> = {
  block: 'danger',
  mask: 'warning',
  audit: 'info',
}

function employeeLabel(id: string | null): string {
  if (!id) return '—'
  const employee = employeeMap.value.get(id)
  return employee ? `${employee.name}（${employee.employee_no}）` : id.slice(0, 8)
}

function statusTagType(code: number): 'success' | 'warning' | 'danger' {
  if (code >= 500) return 'danger'
  if (code >= 400) return 'warning'
  return 'success'
}

function actionLabel(action: string): string {
  return actionLabels[action] ?? (action || '无')
}

function actionTagType(action: string): 'danger' | 'warning' | 'info' {
  return actionTagTypes[action] ?? 'info'
}

async function loadEmployees(): Promise<void> {
  try {
    const response = await api.get<PageResponse<Employee>>(
      '/employees?page=1&page_size=200',
    )
    employees.value = response.items
  } catch (error) {
    ElMessage.warning(apiErrorMessage(error, '员工列表加载失败'))
  }
}

function buildQuery(cursor = ''): string {
  const params = new URLSearchParams()
  params.set('limit', '50')
  if (dateRange.value?.[0]) params.set('from', dateRange.value[0])
  if (dateRange.value?.[1]) params.set('to', dateRange.value[1])
  if (employeeFilter.value) params.set('employee_id', employeeFilter.value)
  if (statusCode.value !== undefined && statusCode.value !== null) {
    params.set('status_code', String(statusCode.value))
  }
  if (dlpAction.value) params.set('dlp_action', dlpAction.value)
  if (modelFilter.value.trim()) {
    params.set('model', modelFilter.value.trim())
  }
  if (cursor) params.set('cursor', cursor)
  return params.toString()
}

async function loadAudit(append = false): Promise<void> {
  if (append && !nextCursor.value) return
  if (append) loadingMore.value = true
  else loading.value = true
  loadError.value = ''
  try {
    const response = await api.get<AuditListResponse>(
      `/audit?${buildQuery(append ? nextCursor.value : '')}`,
    )
    items.value = append ? [...items.value, ...response.items] : response.items
    nextCursor.value = response.next_cursor
  } catch (error) {
    loadError.value = apiErrorMessage(error, '审计日志加载失败')
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

function search(): void {
  nextCursor.value = ''
  void loadAudit(false)
}

function resetFilters(): void {
  dateRange.value = [shanghaiDateDaysAgo(6), shanghaiDateDaysAgo(0)]
  employeeFilter.value = ''
  statusCode.value = undefined
  dlpAction.value = ''
  modelFilter.value = ''
  search()
}

function loadMore(): void {
  void loadAudit(true)
}

async function openDetail(row: AuditLog): Promise<void> {
  detailVisible.value = true
  detailLoading.value = true
  detailError.value = ''
  detailRecord.value = null
  try {
    detailRecord.value = await api.get<AuditLog>(`/audit/${row.id}`)
  } catch (error) {
    detailError.value = apiErrorMessage(error, '审计详情加载失败')
  } finally {
    detailLoading.value = false
  }
}

function closeDetail(): void {
  detailRecord.value = null
  detailError.value = ''
}

function openReveal(row: AuditLog): void {
  if (!canReveal.value) return
  revealRecord.value = row
  revealReason.value = ''
  revealError.value = ''
  revealedPrompt.value = ''
  revealedResponse.value = ''
  hasRevealed.value = false
  revealVisible.value = true
}

function closeReveal(): void {
  revealVisible.value = false
}

function onRevealClosed(): void {
  // Never retain plaintext prompt or response after the dialog is closed.
  revealRecord.value = null
  revealReason.value = ''
  revealError.value = ''
  revealedPrompt.value = ''
  revealedResponse.value = ''
  hasRevealed.value = false
}

async function confirmReveal(): Promise<void> {
  const record = revealRecord.value
  if (!record || revealReasonLength.value < 5) return

  revealLoading.value = true
  revealError.value = ''
  try {
    const result = await api.post<AuditRevealResult>(
      `/audit/${record.id}/reveal`,
      { reason: revealReason.value.trim() },
    )
    revealedPrompt.value = result.prompt
    revealedResponse.value = result.response
    hasRevealed.value = true
  } catch (error) {
    revealError.value = apiErrorMessage(error, '原文查看失败')
  } finally {
    revealLoading.value = false
  }
}

onMounted(() => {
  void loadEmployees()
  void loadAudit(false)
})
</script>

<template>
  <div class="audit-page">
    <section class="page-heading">
      <div>
        <h2>审计日志</h2>
        <p>检索 API 调用、DLP 命中与处置记录。原文默认加密保存且不展示。</p>
      </div>
    </section>

    <el-card class="filter-card" shadow="never">
      <div class="filter-grid">
        <el-date-picker
          v-model="dateRange"
          type="daterange"
          value-format="YYYY-MM-DD"
          range-separator="至"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          class="date-range"
        />
        <el-select
          v-if="employees.length > 0"
          v-model="employeeFilter"
          clearable
          filterable
          placeholder="全部员工"
          class="filter-select"
        >
          <el-option
            v-for="employee in employees"
            :key="employee.id"
            :label="`${employee.name}（${employee.employee_no}）`"
            :value="employee.id"
          />
        </el-select>
        <el-input
          v-else
          disabled
          placeholder="暂无员工"
          class="filter-select"
        />
        <el-input-number
          v-model="statusCode"
          :min="100"
          :max="599"
          :controls="false"
          placeholder="状态码"
          class="status-input"
        />
        <el-select
          v-model="dlpAction"
          placeholder="全部 DLP 动作"
          class="filter-select"
        >
          <el-option label="全部 DLP 动作" value="" />
          <el-option label="无命中" value="none" />
          <el-option label="阻断" value="block" />
          <el-option label="脱敏" value="mask" />
          <el-option label="仅审计" value="audit" />
        </el-select>
        <el-input
          v-model="modelFilter"
          placeholder="模型名称"
          clearable
          class="model-input"
          @keyup.enter="search"
        />
        <el-button type="primary" :icon="Search" @click="search">
          查询
        </el-button>
        <el-button :icon="Refresh" @click="resetFilters">重置</el-button>
      </div>
    </el-card>

    <el-card class="table-card" shadow="never">
      <el-alert
        v-if="loadError"
        :title="loadError"
        type="error"
        :closable="false"
        show-icon
        class="load-error"
      />

      <el-table v-loading="loading" :data="items" stripe>
        <el-table-column label="时间" width="155">
          <template #default="{ row }">{{ formatDateTime(row.event_time) }}</template>
        </el-table-column>
        <el-table-column label="员工" min-width="150">
          <template #default="{ row }">{{ employeeLabel(row.employee_id) }}</template>
        </el-table-column>
        <el-table-column prop="model" label="模型" min-width="130">
          <template #default="{ row }">{{ row.model || '—' }}</template>
        </el-table-column>
        <el-table-column label="请求" min-width="190">
          <template #default="{ row }">
            <span class="method">{{ row.method }}</span>
            <span class="path">{{ row.path }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态码" width="90">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.status_code)" size="small">
              {{ row.status_code }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="DLP" width="90">
          <template #default="{ row }">
            <el-tag
              v-if="row.dlp_action && row.dlp_action !== 'none'"
              :type="actionTagType(row.dlp_action)"
              size="small"
            >
              {{ actionLabel(row.dlp_action) }}
            </el-tag>
            <span v-else class="muted">无</span>
          </template>
        </el-table-column>
        <el-table-column label="Token" width="100" align="right">
          <template #default="{ row }">
            {{ row.total_tokens.toLocaleString('zh-CN') }}
          </template>
        </el-table-column>
        <el-table-column label="耗时" width="90" align="right">
          <template #default="{ row }">{{ row.latency_ms }} ms</template>
        </el-table-column>
        <el-table-column label="操作" width="170" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row)">
              详情
            </el-button>
            <el-button
              v-if="canReveal && (row.has_prompt || row.has_response)"
              link
              type="warning"
              data-test="reveal"
              @click="openReveal(row)"
            >
              查看原文
            </el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无审计记录" :image-size="80" />
        </template>
      </el-table>

      <div v-if="nextCursor" class="load-more">
        <el-button :loading="loadingMore" @click="loadMore">
          加载更多
        </el-button>
      </div>
    </el-card>

    <el-drawer
      v-model="detailVisible"
      title="审计详情"
      size="520px"
      @closed="closeDetail"
    >
      <div v-loading="detailLoading" class="detail-body">
        <el-alert
          v-if="detailError"
          :title="detailError"
          type="error"
          :closable="false"
          show-icon
        />
        <template v-else-if="detailRecord">
          <el-descriptions :column="1" border>
            <el-descriptions-item label="请求 ID">
              <code>{{ detailRecord.request_id }}</code>
            </el-descriptions-item>
            <el-descriptions-item label="时间">
              {{ formatDateTime(detailRecord.event_time) }}
            </el-descriptions-item>
            <el-descriptions-item label="员工">
              {{ employeeLabel(detailRecord.employee_id) }}
            </el-descriptions-item>
            <el-descriptions-item label="客户端 IP">
              {{ detailRecord.client_ip || '—' }}
            </el-descriptions-item>
            <el-descriptions-item label="请求">
              {{ detailRecord.method }} {{ detailRecord.path }}
            </el-descriptions-item>
            <el-descriptions-item label="模型">
              {{ detailRecord.model || '—' }}
            </el-descriptions-item>
            <el-descriptions-item label="状态码">
              {{ detailRecord.status_code }}
              <span v-if="detailRecord.error_code" class="muted">
                （{{ detailRecord.error_code }}）
              </span>
            </el-descriptions-item>
            <el-descriptions-item label="Token">
              输入 {{ detailRecord.prompt_tokens }} / 输出
              {{ detailRecord.completion_tokens }} / 合计
              {{ detailRecord.total_tokens }}
              <el-tag
                v-if="detailRecord.usage_estimated"
                size="small"
                type="info"
                effect="plain"
              >
                估算
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="耗时">
              总计 {{ detailRecord.latency_ms }} ms / 首字节
              {{ detailRecord.first_byte_ms }} ms / 上游
              {{ detailRecord.upstream_latency_ms }} ms
            </el-descriptions-item>
            <el-descriptions-item label="上游请求 ID">
              {{ detailRecord.upstream_request_id || '—' }}
            </el-descriptions-item>
          </el-descriptions>

          <div class="detail-section">
            <h4>DLP 命中</h4>
            <div v-if="detailRecord.dlp_hits.length === 0" class="muted">
              无命中
            </div>
            <div v-else class="hit-tags">
              <el-tag
                v-for="hit in detailRecord.dlp_hits"
                :key="hit.rule_id"
                :type="actionTagType(hit.action)"
                size="small"
              >
                {{ hit.rule_name }} · {{ actionLabel(hit.action) }}
              </el-tag>
            </div>
          </div>

          <div class="detail-section">
            <h4>请求原文</h4>
            <p class="muted">
              原文使用 AES-256-GCM 加密保存，默认不返回。超级管理员查看时必须填写原因并留下审计记录。
            </p>
            <el-button
              v-if="canReveal && (detailRecord.has_prompt || detailRecord.has_response)"
              type="warning"
              data-test="reveal"
              @click="openReveal(detailRecord)"
            >
              查看原文
            </el-button>
            <span v-else class="muted">
              {{ detailRecord.has_prompt || detailRecord.has_response ? '仅超级管理员可查看' : '未保存原文' }}
            </span>
          </div>
        </template>
      </div>
    </el-drawer>

    <el-dialog
      v-model="revealVisible"
      title="查看敏感原文"
      width="720px"
      :close-on-click-modal="false"
      @closed="onRevealClosed"
    >
      <el-alert
        title="此操作会记录管理员账号、时间、来源 IP 与查看原因。查看结果不会缓存。"
        type="warning"
        :closable="false"
        show-icon
        class="reveal-alert"
      />

      <el-form label-position="top" @submit.prevent="confirmReveal">
        <el-form-item label="查看原因（至少 5 个字符）" required>
          <el-input
            v-model="revealReason"
            data-test="reveal-reason"
            type="textarea"
            :rows="3"
            maxlength="500"
            show-word-limit
            placeholder="例如：处理客户投诉，需要核对员工提交的提示词"
          />
        </el-form-item>
      </el-form>

      <el-alert
        v-if="revealError"
        :title="revealError"
        type="error"
        :closable="false"
        show-icon
      />

      <template v-if="hasRevealed">
        <div class="reveal-section">
          <h4>Prompt</h4>
          <pre class="reveal-text">{{ revealedPrompt || '（无内容）' }}</pre>
        </div>
        <div class="reveal-section">
          <h4>Response</h4>
          <pre class="reveal-text">{{ revealedResponse || '（无内容）' }}</pre>
        </div>
      </template>

      <template #footer>
        <el-button @click="closeReveal">关闭</el-button>
        <el-button
          data-test="reveal-confirm"
          type="primary"
          :loading="revealLoading"
          :disabled="revealReasonLength < 5 || revealLoading"
          @click="confirmReveal"
        >
          确认查看
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page-heading {
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

.filter-card,
.table-card {
  border: 1px solid #e8edf4;
  border-radius: 12px;
}

.filter-card {
  margin-bottom: 18px;
}

.filter-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.date-range {
  width: 260px;
}

.filter-select {
  width: 190px;
}

.status-input {
  width: 110px;
}

.model-input {
  width: 170px;
}

.load-error {
  margin-bottom: 16px;
}

.method {
  margin-right: 8px;
  padding: 1px 6px;
  border-radius: 4px;
  background: #eef2f7;
  color: #52637a;
  font-size: 11px;
}

.path {
  color: #52637a;
  font-size: 12px;
  word-break: break-all;
}

.muted {
  color: #98a6b7;
  font-size: 12px;
}

.load-more {
  display: flex;
  justify-content: center;
  margin-top: 16px;
}

.detail-body {
  min-height: 200px;
}

.detail-section {
  margin-top: 22px;
}

.detail-section h4,
.reveal-section h4 {
  margin: 0 0 10px;
  color: #334155;
  font-size: 14px;
}

.hit-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.reveal-alert {
  margin-bottom: 16px;
}

.reveal-section {
  margin-top: 18px;
}

.reveal-text {
  max-height: 240px;
  margin: 0;
  padding: 12px;
  overflow: auto;
  border-radius: 8px;
  background: #16212f;
  color: #d8e4f2;
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', monospace;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

code {
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', monospace;
  font-size: 12px;
}

@media (max-width: 900px) {
  .date-range,
  .filter-select,
  .model-input {
    width: 100%;
  }
}
</style>
