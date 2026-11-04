<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import {
  Coin,
  Lock,
  Refresh,
  Timer,
  TrendCharts,
  User,
  WarningFilled,
} from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  DashboardIncident,
  DashboardRange,
  Department,
  Provider,
} from '../api/types'
import BreakdownPanel from '../components/dashboard/BreakdownPanel.vue'
import DLPPanel from '../components/dashboard/DLPPanel.vue'
import IncidentFeed from '../components/dashboard/IncidentFeed.vue'
import MetricCard from '../components/dashboard/MetricCard.vue'
import ModelUsagePanel from '../components/dashboard/ModelUsagePanel.vue'
import QuotaPressurePanel from '../components/dashboard/QuotaPressurePanel.vue'
import TopUsersTable from '../components/dashboard/TopUsersTable.vue'
import TrendPanel from '../components/dashboard/TrendPanel.vue'
import type { TrendMetric } from '../components/dashboard/TrendPanel.vue'
import {
  useDashboard,
  type DashboardFilters,
} from '../composables/useDashboard'
import { ADMIN_ROOT } from '../router/paths'
import { formatDateTime, toShanghaiDate } from '../utils/datetime'
import {
  formatClock,
  formatDuration,
  formatNumber,
  formatPercent,
} from '../utils/format'

const router = useRouter()

const rangeOptions: Array<{ label: string; value: DashboardRange }> = [
  { label: '今日', value: 'today' },
  { label: '昨天', value: 'yesterday' },
  { label: '近 24 小时', value: '24h' },
  { label: '近 7 天', value: '7d' },
  { label: '近 30 天', value: '30d' },
  { label: '近 90 天', value: '90d' },
  { label: '自定义', value: 'custom' },
]
const rangeValues = new Set<DashboardRange>([
  'today',
  'yesterday',
  '24h',
  '7d',
  '30d',
  '90d',
  'custom',
])

const dayFormat = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
})

function dayKey(date: Date): string {
  return dayFormat.format(date)
}

function shiftDayKey(day: string, delta: number): string {
  const cursor = new Date(`${day}T00:00:00Z`)
  cursor.setUTCDate(cursor.getUTCDate() + delta)
  return cursor.toISOString().slice(0, 10)
}

function parseDateParam(raw: string | null): string | null {
  return raw && /^\d{4}-\d{2}-\d{2}$/.test(raw) ? raw : null
}

function customBounds(range: [string, string]): { from: string; to: string } {
  return {
    from: `${range[0]}T00:00:00+08:00`,
    to: `${range[1]}T23:59:59+08:00`,
  }
}

function initialCustomRange(): [string, string] {
  const params = new URLSearchParams(window.location.search)
  if (params.get('range') !== 'custom') {
    const today = dayKey(new Date())
    return [shiftDayKey(today, -6), today]
  }
  const rawFrom = parseDateParam(params.get('from'))
  const rawTo = parseDateParam(params.get('to'))
  if (rawFrom && rawTo && rawFrom <= rawTo) return [rawFrom, rawTo]
  const today = dayKey(new Date())
  return [shiftDayKey(today, -6), today]
}

function initialFilters(): DashboardFilters {
  const params = new URLSearchParams(window.location.search)
  const rawRange = params.get('range') as DashboardRange | null
  const rawDepartment = Number(params.get('department_id') ?? '')
  const rawFrom = parseDateParam(params.get('from'))
  const rawTo = parseDateParam(params.get('to'))
  let selected: DashboardRange =
    rawRange && rangeValues.has(rawRange) ? rawRange : '24h'
  let bounds: { from: string; to: string } | null = null
  if (selected === 'custom') {
    if (rawFrom && rawTo && rawFrom <= rawTo) {
      bounds = customBounds([rawFrom, rawTo])
    } else {
      selected = '24h'
    }
  }
  return {
    range: selected,
    from: bounds?.from ?? '',
    to: bounds?.to ?? '',
    departmentId:
      Number.isInteger(rawDepartment) && rawDepartment > 0
        ? rawDepartment
        : null,
    providerId: params.get('provider_id') ?? '',
  }
}

const {
  range,
  from,
  to,
  departmentId,
  providerId,
  data,
  loading,
  refreshing,
  error,
  lastUpdated,
  refresh,
} = useDashboard(initialFilters())

const customRange = ref<[string, string]>(initialCustomRange())

watch([range, customRange], () => {
  if (range.value !== 'custom') {
    from.value = ''
    to.value = ''
    return
  }
  const bounds = customBounds(customRange.value)
  from.value = bounds.from
  to.value = bounds.to
})

function disabledDate(date: Date): boolean {
  const today = dayKey(new Date())
  const earliest = shiftDayKey(today, -89)
  const key = dayKey(date)
  return key > today || key < earliest
}

const departments = ref<Department[]>([])
const providers = ref<Provider[]>([])
const trendMetric = ref<TrendMetric>('requests')

const cardMeta = [
  { icon: TrendCharts, tone: 'primary' as const },
  { icon: Coin, tone: 'primary' as const },
  { icon: User, tone: 'success' as const },
  { icon: WarningFilled, tone: 'danger' as const },
  { icon: Lock, tone: 'warning' as const },
  { icon: Timer, tone: 'info' as const },
]

const departmentModel = computed<number | ''>({
  get: () => departmentId.value ?? '',
  set: (value) => {
    departmentId.value = value === '' ? null : Number(value)
  },
})

const providerModel = computed<string>({
  get: () => providerId.value,
  set: (value) => {
    providerId.value = value
  },
})

function persistFilters(): void {
  const params = new URLSearchParams(window.location.search)
  params.set('range', range.value)
  if (range.value === 'custom') {
    params.set('from', customRange.value[0])
    params.set('to', customRange.value[1])
  } else {
    params.delete('from')
    params.delete('to')
  }
  if (departmentId.value !== null) {
    params.set('department_id', String(departmentId.value))
  } else {
    params.delete('department_id')
  }
  if (providerId.value) params.set('provider_id', providerId.value)
  else params.delete('provider_id')
  const query = params.toString()
  const url = `${window.location.pathname}${query ? `?${query}` : ''}${window.location.hash}`
  window.history.replaceState(window.history.state, '', url)
}

watch([range, customRange, departmentId, providerId], persistFilters)

async function loadLookups(): Promise<void> {
  try {
    const [departmentResult, providerResult] = await Promise.all([
      api.get<{ items: Department[] }>('/departments'),
      api.get<{ items: Provider[] }>('/providers'),
    ])
    departments.value = departmentResult.items
    providers.value = providerResult.items
  } catch (err) {
    ElMessage.warning(apiErrorMessage(err, '筛选条件加载失败'))
  }
}

const cards = computed(() => {
  const kpis = data.value?.kpis
  if (!kpis) {
    return [
      '请求量',
      'Token 消耗',
      '活跃员工',
      '错误率',
      'DLP 风险',
      'P95 延迟',
    ].map((label, index) => ({
      label,
      value: '—',
      hint: '正在加载',
      delta: undefined,
      deltaKind: 'pct' as const,
      direction: 'none' as const,
      ...cardMeta[index]!,
    }))
  }
  const items = [
    {
      label: '请求量',
      value: formatNumber(kpis.requests.value),
      hint: `错误 ${formatNumber(Math.round(kpis.error_rate.value * kpis.requests.value))} · 断开 ${formatNumber(kpis.client_disconnects.value)}`,
      delta: kpis.requests.delta_pct,
      deltaKind: 'pct' as const,
      direction: 'none' as const,
    },
    {
      label: 'Token 消耗',
      value: formatNumber(kpis.tokens.value),
      hint: `输入 ${formatNumber(kpis.prompt_tokens.value)} · 输出 ${formatNumber(kpis.completion_tokens.value)}`,
      delta: kpis.tokens.delta_pct,
      deltaKind: 'pct' as const,
      direction: 'none' as const,
    },
    {
      label: '活跃员工',
      value: formatNumber(kpis.active_users.value),
      hint: '产生调用的员工数',
      delta: kpis.active_users.delta_pct,
      deltaKind: 'pct' as const,
      direction: 'up' as const,
    },
    {
      label: '错误率',
      value: formatPercent(kpis.error_rate.value, 2),
      hint: '不含客户端断开（499）',
      delta: kpis.error_rate.delta_pp,
      deltaKind: 'pp' as const,
      direction: 'down' as const,
    },
    {
      label: 'DLP 风险',
      value: formatNumber(kpis.dlp_requests.value),
      hint: `命中 ${formatNumber(kpis.dlp_requests.value ? data.value?.dlp.hits ?? 0 : 0)} 次`,
      delta: kpis.dlp_requests.delta_pct,
      deltaKind: 'pct' as const,
      direction: 'down' as const,
    },
    {
      label: 'P95 延迟',
      value:
        kpis.requests.value > 0
          ? formatDuration(kpis.p95_latency_ms.value)
          : '—',
      hint: '受流式响应影响，仅供参考',
      delta: kpis.p95_latency_ms.delta_pct,
      deltaKind: 'pct' as const,
      direction: 'down' as const,
    },
  ]
  return items.map((item, index) => ({ ...item, ...cardMeta[index]! }))
})

const providerRows = computed(() =>
  (data.value?.breakdown.providers ?? []).map((item) => ({
    key: item.id || 'no-upstream',
    label: item.id ? item.name || '未命名上游' : '未调用上游',
    value: item.requests,
    tokens: item.tokens,
    errors: item.errors,
    latencyMs: item.p95_latency_ms,
  })),
)

const lastUpdatedLabel = computed(() =>
  lastUpdated.value ? formatClock(lastUpdated.value) : '—',
)

async function manualRefresh(): Promise<void> {
  await refresh()
  if (!error.value) ElMessage.success('概览数据已刷新')
}

function auditQuery(extra: Record<string, string>): Record<string, string> {
  const query: Record<string, string> = {}
  const summaryRange = data.value?.range
  if (summaryRange) {
    query.from = toShanghaiDate(summaryRange.from)
    query.to = toShanghaiDate(summaryRange.to)
  }
  for (const [key, value] of Object.entries(extra)) {
    if (value) query[key] = value
  }
  return query
}

function openEmployeeAudit(employeeId: string): void {
  void router.push({
    path: `${ADMIN_ROOT}/audit`,
    query: auditQuery({ employee_id: employeeId }),
  })
}

function openTokens(employeeId: string): void {
  void router.push({
    path: `${ADMIN_ROOT}/tokens`,
    query: { employee_id: employeeId },
  })
}

function openIncident(incident: DashboardIncident): void {
  void router.push({
    path: `${ADMIN_ROOT}/audit`,
    query: auditQuery({
      employee_id: incident.employee_id,
      audit_id: String(incident.id),
    }),
  })
}

onMounted(() => {
  void loadLookups()
})
</script>

<template>
  <div class="dashboard-page">
    <section class="page-heading">
      <div>
        <span class="heading-eyebrow">运行监控</span>
        <h2>运行概览</h2>
        <p>集中查看 API 调用、Token 消耗、DLP 风险与配额压力。</p>
      </div>
      <div class="heading-actions">
        <span class="refresh-hint">
          <span class="refresh-dot" />
          每 60 秒自动刷新 · 更新于 {{ lastUpdatedLabel }}
        </span>
        <el-button
          :icon="Refresh"
          :loading="loading || refreshing"
          @click="manualRefresh"
        >
          刷新
        </el-button>
      </div>
    </section>

    <section class="filter-bar">
      <div class="filter-group">
        <span class="filter-label">时间</span>
        <el-radio-group v-model="range" size="small">
          <el-radio-button
            v-for="option in rangeOptions"
            :key="option.value"
            :value="option.value"
          >
            {{ option.label }}
          </el-radio-button>
        </el-radio-group>
        <el-date-picker
          v-if="range === 'custom'"
          v-model="customRange"
          type="daterange"
          size="small"
          value-format="YYYY-MM-DD"
          range-separator="至"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          :clearable="false"
          :disabled-date="disabledDate"
          class="range-picker"
        />
      </div>
      <span class="filter-divider" />
      <div class="filter-group">
        <span class="filter-label">部门</span>
        <el-select
          v-model="departmentModel"
          clearable
          placeholder="全部部门"
          size="small"
          class="filter-select"
        >
          <el-option
            v-for="department in departments"
            :key="department.id"
            :label="department.name"
            :value="department.id"
          />
        </el-select>
      </div>
      <div class="filter-group">
        <span class="filter-label">上游</span>
        <el-select
          v-model="providerModel"
          clearable
          placeholder="全部上游"
          size="small"
          class="filter-select"
        >
          <el-option
            v-for="provider in providers"
            :key="provider.id"
            :label="provider.name"
            :value="provider.id"
          />
        </el-select>
      </div>
      <span v-if="data" class="range-note">
        {{ formatDateTime(data.range.from) }} — {{ formatDateTime(data.range.to) }}
      </span>
    </section>

    <el-alert
      v-if="error"
      :title="data ? `${error}（显示上次成功加载的数据）` : error"
      type="error"
      :closable="false"
      show-icon
      class="load-error"
    >
      <template #default>
        <el-button link type="primary" @click="manualRefresh">重试</el-button>
      </template>
    </el-alert>

    <div v-if="loading && !data" v-loading="true" class="initial-loading" />

    <template v-else>
      <el-row :gutter="16" class="kpi-row">
        <el-col
          v-for="card in cards"
          :key="card.label"
          :xs="24"
          :sm="12"
          :md="8"
          :lg="4"
        >
          <MetricCard
            :label="card.label"
            :value="card.value"
            :hint="card.hint"
            :delta="card.delta"
            :delta-kind="card.deltaKind"
            :direction="card.direction"
            :icon="card.icon"
            :tone="card.tone"
          />
        </el-col>
      </el-row>

      <template v-if="data">
        <TrendPanel
          v-model:metric="trendMetric"
          :trend="data.trend"
          class="section-row"
        />

        <el-row :gutter="16" class="section-row">
          <el-col :xs="24" :lg="12">
            <BreakdownPanel
              title="上游分布"
              subtitle="按请求量排序"
              :rows="providerRows"
              empty-text="所选范围内无上游调用"
            />
          </el-col>
          <el-col :xs="24" :lg="12">
            <ModelUsagePanel
              :models="data.breakdown.models"
              :buckets="data.trend.buckets"
              :total-tokens="data.kpis.tokens.value"
            />
          </el-col>
        </el-row>

        <el-row :gutter="16" class="section-row">
          <el-col :xs="24" :lg="16">
            <TopUsersTable
              :users="data.top_users"
              @select-user="openEmployeeAudit"
              @view-tokens="openTokens"
            />
          </el-col>
          <el-col :xs="24" :lg="8">
            <DLPPanel :dlp="data.dlp" />
          </el-col>
        </el-row>

        <el-row :gutter="16" class="section-row">
          <el-col :xs="24" :lg="12">
            <QuotaPressurePanel
              :items="data.quota_pressure"
              @view-tokens="openTokens"
            />
          </el-col>
          <el-col :xs="24" :lg="12">
            <IncidentFeed
              :incidents="data.recent_incidents"
              @select-incident="openIncident"
            />
          </el-col>
        </el-row>
      </template>

      <el-empty
        v-else
        description="暂无概览数据"
        :image-size="90"
        class="empty-page"
      />
    </template>
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

.heading-eyebrow {
  display: block;
  margin-bottom: 7px;
  color: #96a4b7;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.14em;
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
  gap: 14px;
}

.refresh-hint {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: #94a2b4;
  font-size: 11px;
  white-space: nowrap;
}

.refresh-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #1aa06d;
  box-shadow: 0 0 0 3px rgba(26, 160, 109, 0.12);
}

.filter-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 14px;
  margin-bottom: 18px;
  padding: 12px 16px;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-md);
  background: #fff;
  box-shadow: 0 1px 2px rgba(23, 32, 51, 0.03);
}

.filter-group {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 9px;
}

.range-picker {
  width: 236px;
}

.filter-label {
  color: #8b98aa;
  font-size: 11px;
  font-weight: 500;
  white-space: nowrap;
}

.filter-divider {
  width: 1px;
  height: 20px;
  background: var(--gw-border);
}

.filter-bar :deep(.el-radio-group) {
  padding: 2px;
  border-radius: 8px;
  background: #f4f7fb;
}

.filter-bar :deep(.el-radio-button__inner) {
  border: none;
  border-radius: 6px !important;
  background: transparent;
  box-shadow: none;
  color: var(--gw-text-secondary);
  font-size: 11px;
  padding: 5px 10px;
}

.filter-bar :deep(.el-radio-button.is-active .el-radio-button__inner) {
  background: #fff;
  box-shadow: 0 1px 3px rgba(23, 32, 51, 0.12);
  color: var(--gw-primary);
}

.filter-select {
  width: 150px;
}

.range-note {
  margin-left: auto;
  color: #9aa7b8;
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}

.load-error {
  margin-bottom: 16px;
  border-radius: var(--gw-radius-md);
}

.initial-loading {
  min-height: 320px;
}

.kpi-row {
  margin-bottom: 2px;
}

.kpi-row :deep(.el-col) {
  margin-bottom: 16px;
}

.section-row {
  margin-top: 0;
  margin-bottom: 16px;
}

.section-row :deep(.el-col) {
  margin-bottom: 16px;
}

.empty-page {
  margin-top: 60px;
}

@media (max-width: 900px) {
  .page-heading {
    flex-direction: column;
  }

  .heading-actions {
    width: 100%;
    justify-content: space-between;
  }

  .range-note {
    width: 100%;
    margin-left: 0;
  }

  .filter-divider {
    display: none;
  }

  .filter-group {
    width: 100%;
  }

  .filter-group :deep(.el-radio-group) {
    flex: 1;
  }

  .filter-group :deep(.el-radio-button) {
    flex: 1;
  }

  .filter-group :deep(.el-radio-button__inner) {
    width: 100%;
  }

  .filter-select {
    flex: 1;
    width: auto;
  }
}
</style>
