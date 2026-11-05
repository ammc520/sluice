<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { CopyDocument, Refresh, Search } from '@element-plus/icons-vue'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  AuditListResponse,
  AuditLog,
  AuditRevealResult,
  Department,
  Employee,
  PageResponse,
} from '../api/types'
import { useAuthStore } from '../stores/auth'
import {
  formatDateTime,
  shanghaiDateDaysAgo,
  toShanghaiDate,
} from '../utils/datetime'
import { formatNumber } from '../utils/format'
import {
  formatPrompt,
  formatResponse,
  prettyJSON,
} from '../utils/promptFormat'

type DLPActionFilter = '' | 'none' | 'block' | 'mask' | 'audit'

function queryDate(value: string | null): string {
  if (!value) return ''
  return value.length === 10 ? value : toShanghaiDate(value)
}

function queryStatus(value: string | null): number | undefined {
  if (!value) return undefined
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed >= 100 && parsed <= 599
    ? parsed
    : undefined
}

function queryDLPAction(value: string | null): DLPActionFilter {
  return value === 'none' || value === 'block' || value === 'mask' || value === 'audit'
    ? value
    : ''
}

const initialQuery = new URLSearchParams(window.location.search)
const initialFrom = queryDate(initialQuery.get('from'))
const initialTo = queryDate(initialQuery.get('to'))
const initialAuditID = Number(initialQuery.get('audit_id'))

const auth = useAuthStore()
const canReveal = computed(() => auth.isSuperAdmin)

const employees = ref<Employee[]>([])
const departments = ref<Department[]>([])
const employeeMap = computed(
  () => new Map(employees.value.map((employee) => [employee.id, employee])),
)

const dateRange = ref<[string, string] | null>(
  initialFrom && initialTo
    ? [initialFrom, initialTo]
    : [shanghaiDateDaysAgo(6), shanghaiDateDaysAgo(0)],
)
const employeeFilter = ref(initialQuery.get('employee_id') ?? '')
const departmentFilter = ref(initialQuery.get('department_id') ?? '')
const statusCode = ref<number | undefined>(
  queryStatus(initialQuery.get('status_code')),
)
const dlpAction = ref<DLPActionFilter>(
  queryDLPAction(initialQuery.get('dlp_action')),
)
const modelFilter = ref(initialQuery.get('model') ?? '')

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
const revealTab = ref<'structured' | 'raw'>('structured')

const revealReasonLength = computed(
  () => Array.from(revealReason.value.trim()).length,
)

const revealPromptView = computed(() => formatPrompt(revealedPrompt.value))
const revealResponseView = computed(() => formatResponse(revealedResponse.value))
const revealPromptRaw = computed(() => prettyJSON(revealedPrompt.value))
const revealResponseRaw = computed(() => prettyJSON(revealedResponse.value))

const revealUsage = computed(() => {
  const view = revealResponseView.value
  const parts: string[] = []
  if (view.inputTokens !== null) {
    parts.push(`输入 ${formatNumber(view.inputTokens)}`)
  }
  if (view.outputTokens !== null) {
    parts.push(`输出 ${formatNumber(view.outputTokens)}`)
  }
  if (view.cacheReadTokens !== null) {
    parts.push(`缓存读 ${formatNumber(view.cacheReadTokens)}`)
  }
  if (view.cacheCreationTokens !== null) {
    parts.push(`缓存写 ${formatNumber(view.cacheCreationTokens)}`)
  }
  return parts.join(' · ')
})

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

const roleLabels: Record<string, string> = {
  system: '系统',
  user: '用户',
  assistant: '助手',
  tool: '工具',
  unknown: '未知角色',
}

function roleLabel(role: string): string {
  return roleLabels[role] ?? role
}

function roleTagType(role: string): 'primary' | 'success' | 'warning' | 'info' {
  if (role === 'user') return 'primary'
  if (role === 'assistant') return 'success'
  if (role === 'tool') return 'warning'
  return 'info'
}

async function writeClipboard(text: string): Promise<boolean> {
  if (window.isSecureContext && navigator.clipboard) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // Fall through to the legacy path for HTTP-only deployments.
    }
  }
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  let copied = false
  try {
    copied = document.execCommand('copy')
  } catch {
    copied = false
  }
  document.body.removeChild(textarea)
  return copied
}

async function copyRevealed(text: string, label: string): Promise<void> {
  if (!text) return
  if (await writeClipboard(text)) {
    ElMessage.success(`${label}已复制`)
  } else {
    ElMessage.error('复制失败，请手动选择文本')
  }
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

async function loadDepartments(): Promise<void> {
  try {
    const response = await api.get<{ items: Department[] }>('/departments')
    departments.value = response.items
  } catch (error) {
    ElMessage.warning(apiErrorMessage(error, '部门列表加载失败'))
  }
}

function buildQuery(cursor = ''): string {
  const params = new URLSearchParams()
  params.set('limit', '50')
  if (dateRange.value?.[0]) params.set('from', dateRange.value[0])
  if (dateRange.value?.[1]) params.set('to', dateRange.value[1])
  if (employeeFilter.value) params.set('employee_id', employeeFilter.value)
  if (departmentFilter.value) params.set('department_id', departmentFilter.value)
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

function persistFilters(): void {
  const params = new URLSearchParams()
  if (dateRange.value?.[0]) params.set('from', dateRange.value[0])
  if (dateRange.value?.[1]) params.set('to', dateRange.value[1])
  if (employeeFilter.value) params.set('employee_id', employeeFilter.value)
  if (departmentFilter.value) {
    params.set('department_id', departmentFilter.value)
  }
  if (statusCode.value !== undefined && statusCode.value !== null) {
    params.set('status_code', String(statusCode.value))
  }
  if (dlpAction.value) params.set('dlp_action', dlpAction.value)
  if (modelFilter.value.trim()) {
    params.set('model', modelFilter.value.trim())
  }
  const query = params.toString()
  const url = `${window.location.pathname}${query ? `?${query}` : ''}`
  window.history.replaceState(window.history.state, '', url)
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
  persistFilters()
  void loadAudit(false)
}

function resetFilters(): void {
  dateRange.value = [shanghaiDateDaysAgo(6), shanghaiDateDaysAgo(0)]
  employeeFilter.value = ''
  departmentFilter.value = ''
  statusCode.value = undefined
  dlpAction.value = ''
  modelFilter.value = ''
  search()
}

function loadMore(): void {
  void loadAudit(true)
}

async function openDetail(row: Pick<AuditLog, 'id'>): Promise<void> {
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
  revealTab.value = 'structured'
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
  revealTab.value = 'structured'
}

async function confirmReveal(): Promise<void> {
  const record = revealRecord.value
  if (!record || hasRevealed.value || revealReasonLength.value < 5) return

  revealLoading.value = true
  revealError.value = ''
  try {
    const result = await api.post<AuditRevealResult>(
      `/audit/${record.id}/reveal`,
      { reason: revealReason.value.trim() },
    )
    revealedPrompt.value = result.prompt ?? ''
    revealedResponse.value = result.response ?? ''
    hasRevealed.value = true

    const structured = formatPrompt(revealedPrompt.value).parsed
      || formatResponse(revealedResponse.value).parsed
    revealTab.value = structured ? 'structured' : 'raw'
  } catch (error) {
    revealError.value = apiErrorMessage(error, '原文查看失败')
  } finally {
    revealLoading.value = false
  }
}

onMounted(async () => {
  await Promise.all([loadEmployees(), loadDepartments()])
  await loadAudit(false)
  if (Number.isInteger(initialAuditID) && initialAuditID > 0) {
    await openDetail({ id: initialAuditID })
  }
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
          class="filter-select employee-filter"
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
          class="filter-select employee-filter"
        />
        <el-select
          v-if="departments.length > 0"
          v-model="departmentFilter"
          clearable
          filterable
          placeholder="全部部门"
          class="filter-select department-filter"
        >
          <el-option
            v-for="department in departments"
            :key="department.id"
            :label="department.name"
            :value="String(department.id)"
          />
        </el-select>
        <el-input
          v-else
          disabled
          placeholder="暂无部门"
          class="filter-select department-filter"
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
          class="filter-select dlp-filter"
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
        <div class="filter-actions">
          <el-button type="primary" :icon="Search" @click="search">
            查询
          </el-button>
          <el-button :icon="Refresh" @click="resetFilters">重置</el-button>
        </div>
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
        <el-table-column label="时间" width="148">
          <template #default="{ row }">{{ formatDateTime(row.event_time) }}</template>
        </el-table-column>
        <el-table-column label="员工" min-width="128">
          <template #default="{ row }">{{ employeeLabel(row.employee_id) }}</template>
        </el-table-column>
        <el-table-column prop="model" label="模型" min-width="112">
          <template #default="{ row }">{{ row.model || '—' }}</template>
        </el-table-column>
        <el-table-column label="请求" min-width="170">
          <template #default="{ row }">
            <span class="method">{{ row.method }}</span>
            <span class="path">{{ row.path }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态码" width="76">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.status_code)" size="small">
              {{ row.status_code }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="DLP" width="76">
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
        <el-table-column label="Token" width="84" align="right">
          <template #default="{ row }">
            {{ row.total_tokens.toLocaleString('zh-CN') }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="132" align="right">
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
      title="查看请求原文"
      width="min(960px, 94vw)"
      :close-on-click-modal="false"
      class="reveal-dialog"
      @closed="onRevealClosed"
    >
      <template v-if="revealRecord">
        <div class="reveal-context">
          <span class="reveal-context-item">
            <span class="context-label">员工</span>
            {{ employeeLabel(revealRecord.employee_id) }}
          </span>
          <span class="reveal-context-item">
            <span class="context-label">模型</span>
            {{ revealRecord.model || '—' }}
          </span>
          <span class="reveal-context-item">
            <span class="context-label">时间</span>
            {{ formatDateTime(revealRecord.event_time) }}
          </span>
          <span class="reveal-context-item reveal-request-id">
            <span class="context-label">请求 ID</span>
            <code :title="revealRecord.request_id">{{ revealRecord.request_id }}</code>
          </span>
          <span
            v-for="(hit, index) in revealRecord.dlp_hits"
            :key="`${hit.rule_id}-${index}`"
            class="reveal-context-item"
          >
            <el-tag :type="actionTagType(hit.action)" size="small" effect="plain">
              DLP · {{ hit.rule_name }}（{{ actionLabel(hit.action) }}）
            </el-tag>
          </span>
        </div>

        <el-alert
          title="此操作会记录管理员账号、时间、来源 IP 与查看原因。查看结果不会缓存，关闭弹窗即清除。"
          type="warning"
          :closable="false"
          show-icon
          class="reveal-alert"
        />

        <el-form
          v-if="!hasRevealed"
          label-position="top"
          @submit.prevent="confirmReveal"
        >
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
          <div class="reveal-toolbar">
            <el-radio-group v-model="revealTab" size="small">
              <el-radio-button value="structured">结构化</el-radio-button>
              <el-radio-button value="raw">原始数据</el-radio-button>
            </el-radio-group>
            <span class="reveal-toolbar-hint">关闭后立即清除，不缓存</span>
          </div>

          <div v-if="revealTab === 'structured'" class="reveal-panels">
            <section class="reveal-panel">
              <header class="panel-head">
                <h4>提示词</h4>
                <el-button
                  v-if="revealedPrompt"
                  text
                  size="small"
                  :icon="CopyDocument"
                  @click="copyRevealed(revealedPrompt, '提示词原文')"
                >
                  复制原文
                </el-button>
              </header>

              <div v-if="!revealedPrompt" class="reveal-empty">
                该请求没有保存提示词。
              </div>
              <template v-else-if="revealPromptView.parsed">
                <details v-if="revealPromptView.system" class="message-block">
                  <summary>
                    系统提示词 · {{ revealPromptView.system.length }} 字符
                  </summary>
                  <div class="message-text">{{ revealPromptView.system }}</div>
                </details>

                <div
                  v-for="(message, index) in revealPromptView.messages"
                  :key="index"
                  class="message-card"
                  :class="`role-${message.role}`"
                >
                  <div class="message-role">
                    <el-tag
                      size="small"
                      effect="plain"
                      :type="roleTagType(message.role)"
                    >
                      {{ roleLabel(message.role) }}
                    </el-tag>
                    <span class="message-index">#{{ index + 1 }}</span>
                  </div>
                  <div class="message-text">{{ message.text }}</div>
                </div>

                <p
                  v-if="revealPromptView.messages.length === 0"
                  class="reveal-empty"
                >
                  仅包含系统提示词。
                </p>
              </template>
              <template v-else>
                <p class="reveal-note">未能解析为消息结构，按原始文本显示。</p>
                <pre class="reveal-text">{{ revealedPrompt }}</pre>
              </template>
            </section>

            <section class="reveal-panel">
              <header class="panel-head">
                <h4>响应</h4>
                <el-button
                  v-if="revealedResponse"
                  text
                  size="small"
                  :icon="CopyDocument"
                  @click="copyRevealed(revealedResponse, '响应原文')"
                >
                  复制原文
                </el-button>
              </header>

              <div v-if="!revealedResponse" class="reveal-empty">
                该请求没有保存响应。
              </div>
              <template v-else-if="revealResponseView.parsed">
                <div class="response-meta">
                  <el-tag
                    v-if="revealResponseView.streamed"
                    size="small"
                    effect="plain"
                    type="info"
                  >
                    流式响应
                  </el-tag>
                  <el-tag
                    v-if="revealResponseView.model"
                    size="small"
                    effect="plain"
                  >
                    {{ revealResponseView.model }}
                  </el-tag>
                  <el-tag
                    v-if="revealResponseView.stopReason"
                    size="small"
                    effect="plain"
                  >
                    结束原因：{{ revealResponseView.stopReason }}
                  </el-tag>
                  <span v-if="revealUsage" class="response-usage">
                    {{ revealUsage }}
                  </span>
                </div>

                <details
                  v-if="revealResponseView.thinking"
                  class="message-block"
                >
                  <summary>
                    思考过程 · {{ revealResponseView.thinking.length }} 字符
                  </summary>
                  <div class="message-text">{{ revealResponseView.thinking }}</div>
                </details>

                <div
                  v-if="revealResponseView.text"
                  class="message-card role-assistant"
                >
                  <div class="message-role">
                    <el-tag size="small" effect="plain" type="success">助手</el-tag>
                  </div>
                  <div class="message-text">{{ revealResponseView.text }}</div>
                </div>

                <p
                  v-if="!revealResponseView.text && !revealResponseView.thinking"
                  class="reveal-empty"
                >
                  响应中没有可展示的文本。
                </p>
              </template>
              <template v-else>
                <p class="reveal-note">未能解析为消息结构，按原始文本显示。</p>
                <pre class="reveal-text">{{ revealedResponse }}</pre>
              </template>
            </section>
          </div>

          <div v-else class="reveal-panels">
            <section class="reveal-panel">
              <header class="panel-head">
                <h4>提示词 · 原始数据</h4>
                <el-button
                  v-if="revealedPrompt"
                  text
                  size="small"
                  :icon="CopyDocument"
                  @click="copyRevealed(revealedPrompt, '提示词原文')"
                >
                  复制原文
                </el-button>
              </header>
              <pre v-if="revealedPrompt" class="reveal-text reveal-code">{{
                revealPromptRaw
              }}</pre>
              <div v-else class="reveal-empty">该请求没有保存提示词。</div>
            </section>

            <section class="reveal-panel">
              <header class="panel-head">
                <h4>响应 · 原始数据</h4>
                <el-button
                  v-if="revealedResponse"
                  text
                  size="small"
                  :icon="CopyDocument"
                  @click="copyRevealed(revealedResponse, '响应原文')"
                >
                  复制原文
                </el-button>
              </header>
              <pre v-if="revealedResponse" class="reveal-text reveal-code">{{
                revealResponseRaw
              }}</pre>
              <div v-else class="reveal-empty">该请求没有保存响应。</div>
            </section>
          </div>
        </template>
      </template>

      <template #footer>
        <span v-if="!hasRevealed" class="reveal-footer-hint">
          仅超级管理员可查看，操作会写入审计日志
        </span>
        <el-button @click="closeReveal">{{ hasRevealed ? '关闭' : '取消' }}</el-button>
        <el-button
          v-if="!hasRevealed"
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

.filter-card,
.table-card {
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.filter-card :deep(.el-card__body),
.table-card :deep(.el-card__body) {
  padding: 20px 22px;
}

.filter-card {
  margin-bottom: 18px;
}

.filter-grid {
  display: grid;
  grid-template-columns: repeat(12, minmax(0, 1fr));
  gap: 10px;
  align-items: center;
}

.filter-grid :deep(.date-range) {
  grid-column: span 3;
  width: 100%;
}

.employee-filter {
  grid-column: span 3;
  width: 100%;
}

.department-filter {
  grid-column: span 2;
  width: 100%;
}

.status-input {
  grid-column: span 2;
  width: 100%;
}

.dlp-filter {
  grid-column: span 2;
  width: 100%;
}

.model-input {
  grid-column: span 4;
  width: 100%;
}

.filter-actions {
  display: flex;
  grid-column: span 8;
  justify-content: flex-end;
  gap: 10px;
}

.load-error {
  margin-bottom: 16px;
}

.method {
  margin-right: 8px;
  padding: 2px 7px;
  border-radius: 5px;
  background: #eef3fa;
  color: var(--gw-text-secondary);
  font-size: 10px;
  font-weight: 600;
}

.path {
  color: var(--gw-text-secondary);
  font-size: 11px;
  word-break: break-all;
}

.muted {
  color: var(--gw-text-muted);
  font-size: 11px;
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

.detail-section h4 {
  margin: 0 0 10px;
  color: #33415c;
  font-size: 13px;
  font-weight: 600;
}

.hit-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.reveal-context {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 16px;
  margin-bottom: 14px;
  padding: 10px 14px;
  border: 1px solid var(--gw-border);
  border-radius: 10px;
  background: #f8fafc;
  color: var(--gw-text-secondary);
  font-size: 12px;
}

.reveal-context-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.context-label {
  color: var(--gw-text-muted);
  font-size: 11px;
}

.reveal-request-id code {
  display: inline-block;
  max-width: 220px;
  overflow: hidden;
  color: #33415c;
  font-size: 11px;
  text-overflow: ellipsis;
  vertical-align: bottom;
  white-space: nowrap;
}

.reveal-alert {
  margin-bottom: 16px;
}

.reveal-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

.reveal-toolbar-hint {
  color: var(--gw-text-muted);
  font-size: 11px;
}

.reveal-panels {
  display: flex;
  flex-direction: column;
  gap: 18px;
  max-height: 52vh;
  overflow: auto;
  padding-right: 4px;
}

.reveal-panel {
  padding: 14px 16px;
  border: 1px solid var(--gw-border);
  border-radius: 12px;
  background: #fff;
}

.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.panel-head h4 {
  margin: 0;
  color: #33415c;
  font-size: 13px;
  font-weight: 600;
}

.message-card {
  margin-top: 10px;
  padding: 12px 14px;
  border: 1px solid #e3e9f2;
  border-left: 3px solid #c7d2e3;
  border-radius: 10px;
  background: #f7f9fc;
}

.message-card.role-user {
  border-left-color: #3b82f6;
  background: #f5f9ff;
}

.message-card.role-assistant {
  border-left-color: #10b981;
  background: #f4fbf7;
}

.message-card.role-system {
  border-left-color: #94a3b8;
}

.message-role {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.message-index {
  color: var(--gw-text-muted);
  font-size: 11px;
}

.message-text {
  color: #26354c;
  font-size: 13px;
  line-height: 1.65;
  white-space: pre-wrap;
  word-break: break-word;
}

.message-block {
  margin-top: 10px;
  padding: 10px 14px;
  border: 1px dashed #d8e1ee;
  border-radius: 10px;
  background: #fafbfd;
}

.message-block summary {
  cursor: pointer;
  color: #51627d;
  font-size: 12px;
  font-weight: 500;
}

.message-block .message-text {
  margin-top: 10px;
}

.response-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.response-usage {
  color: var(--gw-text-muted);
  font-size: 11px;
}

.reveal-empty {
  padding: 6px 0;
  color: var(--gw-text-muted);
  font-size: 12px;
}

.reveal-note {
  margin: 0 0 8px;
  color: var(--gw-text-muted);
  font-size: 11px;
}

.reveal-text {
  max-height: 320px;
  margin: 0;
  padding: 14px;
  overflow: auto;
  border: 1px solid #e3e9f2;
  border-radius: 10px;
  background: #f7f9fc;
  color: #26354c;
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', monospace;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

.reveal-code {
  border-color: #16212f;
  background: #16212f;
  color: #d8e4f2;
}

.reveal-footer-hint {
  margin-right: auto;
  color: var(--gw-text-muted);
  font-size: 11px;
}

code {
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', monospace;
  font-size: 12px;
}

@media (max-width: 1180px) {
  .filter-grid {
    grid-template-columns: repeat(6, minmax(0, 1fr));
  }

  .filter-grid :deep(.date-range),
  .employee-filter,
  .model-input {
    grid-column: span 3;
  }

  .department-filter,
  .status-input,
  .dlp-filter {
    grid-column: span 2;
  }

  .filter-actions {
    grid-column: span 6;
  }
}

@media (max-width: 720px) {
  .filter-grid {
    grid-template-columns: 1fr;
  }

  .filter-grid :deep(.date-range),
  .employee-filter,
  .department-filter,
  .status-input,
  .dlp-filter,
  .model-input,
  .filter-actions {
    grid-column: span 1;
  }

  .filter-actions .el-button {
    flex: 1;
  }
}
</style>
