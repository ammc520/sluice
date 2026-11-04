<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ArrowRight, Refresh } from '@element-plus/icons-vue'

import ModelUsageCard from '../components/portal/ModelUsageCard.vue'
import UsageTrendChart from '../components/portal/UsageTrendChart.vue'
import { portalApi } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  PortalQuota,
  PortalSummaryResponse,
  PortalTrendMetric,
} from '../api/types'
import { formatDateTime } from '../utils/datetime'
import { formatClock, formatNumber } from '../utils/format'

type RangePreset = 'today' | 'yesterday' | '7d' | '30d' | '90d' | 'custom'

const preset = ref<RangePreset>('30d')
const customRange = ref<[string, string] | null>(null)
const metric = ref<PortalTrendMetric>('requests')
const loading = ref(false)
const loadError = ref('')
const data = ref<PortalSummaryResponse | null>(null)
const quota = ref<PortalQuota | null>(null)
const updatedAt = ref<Date | null>(null)

const presetOptions: Array<{ label: string; value: RangePreset }> = [
  { label: '今日', value: 'today' },
  { label: '昨天', value: 'yesterday' },
  { label: '近 7 天', value: '7d' },
  { label: '近 30 天', value: '30d' },
  { label: '近 90 天', value: '90d' },
  { label: '自定义', value: 'custom' },
]

const metricOptions: Array<{ label: string; value: PortalTrendMetric }> = [
  { label: '调用次数', value: 'requests' },
  { label: 'Tokens', value: 'tokens' },
]

const summary = computed(() => data.value?.summary ?? null)
const models = computed(() => data.value?.models ?? [])
const displayedModels = computed(() => models.value.slice(0, 6))

const rangeLabel = computed(() => {
  switch (preset.value) {
    case 'today':
      return '今日'
    case 'yesterday':
      return '昨天'
    case '7d':
      return '近 7 天'
    case '30d':
      return '近 30 天'
    case '90d':
      return '近 90 天'
    case 'custom':
      return customRange.value
        ? `${customRange.value[0]} ~ ${customRange.value[1]}`
        : '自定义'
  }
})

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

function startOfShanghaiDay(day: string): Date {
  return new Date(`${day}T00:00:00+08:00`)
}

function endOfShanghaiDay(day: string): Date {
  return new Date(`${day}T23:59:59.999+08:00`)
}

function resolveRange(): { from: Date; to: Date } | null {
  const now = new Date()
  const today = dayKey(now)
  switch (preset.value) {
    case 'today':
      return { from: startOfShanghaiDay(today), to: now }
    case 'yesterday': {
      const yesterday = shiftDayKey(today, -1)
      return {
        from: startOfShanghaiDay(yesterday),
        to: endOfShanghaiDay(yesterday),
      }
    }
    case '7d':
      return { from: startOfShanghaiDay(shiftDayKey(today, -6)), to: now }
    case '30d':
      return { from: startOfShanghaiDay(shiftDayKey(today, -29)), to: now }
    case '90d':
      return { from: startOfShanghaiDay(shiftDayKey(today, -89)), to: now }
    case 'custom': {
      const range = customRange.value
      if (!range || !range[0] || !range[1]) return null
      return {
        from: startOfShanghaiDay(range[0]),
        to: endOfShanghaiDay(range[1]),
      }
    }
  }
}

function defaultCustomRange(): [string, string] {
  const today = dayKey(new Date())
  return [shiftDayKey(today, -6), today]
}

function disabledDate(date: Date): boolean {
  const today = dayKey(new Date())
  const earliest = shiftDayKey(today, -89)
  const key = dayKey(date)
  return key > today || key < earliest
}

function onPresetChange(value: string | number | boolean | undefined): void {
  preset.value = value as RangePreset
  if (preset.value === 'custom' && !customRange.value) {
    customRange.value = defaultCustomRange()
  }
  if (!resolveRange()) return
  void load()
}

function onCustomRangeChange(): void {
  if (preset.value !== 'custom' || !resolveRange()) return
  void load()
}

function buildDays(from?: string, to?: string): string[] {
  if (!from || !to) return []
  const start = dayFormat.format(new Date(from))
  const end = dayFormat.format(new Date(to))
  const out: string[] = []
  const cursor = new Date(`${start}T00:00:00Z`)
  const last = new Date(`${end}T00:00:00Z`)
  while (cursor <= last) {
    out.push(cursor.toISOString().slice(0, 10))
    cursor.setUTCDate(cursor.getUTCDate() + 1)
  }
  return out
}

const days = computed(() => buildDays(data.value?.from, data.value?.to))

const trendByDay = computed(
  () => new Map((data.value?.trend ?? []).map((point) => [point.day, point])),
)

const requestSeries = computed(() =>
  days.value.map((day) => trendByDay.value.get(day)?.requests ?? 0),
)

const tokenSeries = computed(() =>
  days.value.map((day) => trendByDay.value.get(day)?.tokens ?? 0),
)

const hasTraffic = computed(() => (summary.value?.requests ?? 0) > 0)

const successCount = computed(() => {
  const current = summary.value
  if (!current) return 0
  return Math.max(0, current.requests - current.errors)
})

const errorRate = computed(() => {
  const current = summary.value
  if (!current || current.requests === 0) return 0
  return (current.errors / current.requests) * 100
})

const errorRateText = computed(() => {
  if (!summary.value || summary.value.requests === 0) return '—'
  return `${errorRate.value.toFixed(errorRate.value >= 10 ? 0 : 1)}%`
})

const metricLabel = computed(
  () => metricOptions.find((option) => option.value === metric.value)?.label ?? '调用次数',
)

const metricTotal = computed(() =>
  metric.value === 'requests'
    ? formatNumber(summary.value?.requests)
    : formatNumber(summary.value?.total_tokens),
)

const quotaTypeLabel = computed(() =>
  quota.value?.quota_type === 'requests' ? '请求数' : 'Tokens',
)

const quotaPeriodLabel = computed(() => {
  switch (quota.value?.quota_period) {
    case 'daily':
      return '每日额度'
    case 'monthly':
      return '每月额度'
    default:
      return '总额度'
  }
})

const quotaPercent = computed(() => {
  const current = quota.value
  if (!current || current.quota_total <= 0) return 0
  return Math.min(100, Math.round((current.quota_used / current.quota_total) * 100))
})

const quotaResetText = computed(() => {
  const current = quota.value
  if (!current) return '—'
  if (current.quota_period === 'total') return '不自动重置'
  return current.resets_at ? formatDateTime(current.resets_at) : '—'
})

function shareOfTokens(tokens: number): number {
  const total = summary.value?.total_tokens ?? 0
  return total > 0 ? tokens / total : 0
}

function onMetricChange(value: string | number | boolean | undefined): void {
  metric.value = value as PortalTrendMetric
}

async function load(): Promise<void> {
  const range = resolveRange()
  if (!range) return
  loading.value = true
  loadError.value = ''
  try {
    const params = new URLSearchParams({
      from: range.from.toISOString(),
      to: range.to.toISOString(),
    })
    const [summary, current] = await Promise.all([
      portalApi.get<PortalSummaryResponse>(`/me/summary?${params.toString()}`),
      portalApi.get<PortalQuota>('/me/quota'),
    ])
    data.value = summary
    quota.value = current
    updatedAt.value = new Date()
  } catch (error) {
    loadError.value = apiErrorMessage(error, '用量概览加载失败')
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="overview-page" v-loading="loading">
    <section class="page-heading">
      <div>
        <h2>我的用量概览</h2>
        <p>仅统计你自己的网关调用，不包含提示词或模型响应内容。</p>
      </div>
    </section>

    <div class="filter-bar">
      <div class="filter-group">
        <span class="filter-label">时间维度</span>
        <el-radio-group v-model="preset" size="small" @change="onPresetChange">
          <el-radio-button
            v-for="option in presetOptions"
            :key="option.value"
            :value="option.value"
          >
            {{ option.label }}
          </el-radio-button>
        </el-radio-group>
        <el-date-picker
          v-if="preset === 'custom'"
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
          @change="onCustomRangeChange"
        />
      </div>
      <div class="filter-actions">
        <span v-if="updatedAt" class="updated-at">
          更新于 {{ formatClock(updatedAt) }}
        </span>
        <el-button
          circle
          :icon="Refresh"
          :loading="loading"
          aria-label="刷新用量数据"
          @click="load"
        />
      </div>
    </div>

    <el-alert
      v-if="loadError"
      :title="loadError"
      type="error"
      :closable="false"
      show-icon
      class="load-error"
    />

    <el-card v-if="quota" class="quota-card" shadow="never">
      <div class="quota-block">
        <div class="quota-heading">
          <span class="quota-label">个人额度 · {{ quotaPeriodLabel }}</span>
          <el-tag
            :type="quotaPercent >= 100 ? 'danger' : quotaPercent >= 80 ? 'warning' : 'success'"
            size="small"
            effect="light"
          >
            已用 {{ quotaPercent }}%
          </el-tag>
        </div>
        <div class="quota-value">
          {{ formatNumber(quota.quota_remaining) }}
          <span class="quota-unit">剩余 {{ quotaTypeLabel }}</span>
        </div>
        <el-progress
          :percentage="quotaPercent"
          :status="quotaPercent >= 100 ? 'exception' : quotaPercent >= 80 ? 'warning' : ''"
          :stroke-width="6"
          :show-text="false"
        />
        <div class="quota-meta">
          <span>
            已用 {{ formatNumber(quota.quota_used) }} /
            {{ formatNumber(quota.quota_total) }} {{ quotaTypeLabel }}
          </span>
          <span>下次重置 {{ quotaResetText }}</span>
        </div>
      </div>
      <div class="quota-side">
        <span class="quota-keys">
          有效 Key {{ quota.active_tokens }} / {{ quota.max_active_tokens }}
        </span>
        <div class="quota-links">
          <el-button text type="primary" @click="$router.push('/portal/tokens')">
            管理 Key
          </el-button>
          <el-button
            v-if="quota.can_request_increase"
            text
            type="primary"
            @click="$router.push('/portal/requests')"
          >
            申请提额
          </el-button>
        </div>
      </div>
    </el-card>

    <div class="metrics">
      <article class="metric-tile tone-requests">
        <span class="metric-label">API 请求次数</span>
        <div class="metric-value">
          {{ formatNumber(summary?.requests) }}<span class="metric-unit">次</span>
        </div>
        <span class="metric-hint">
          成功 {{ formatNumber(successCount) }} · 失败
          {{ formatNumber(summary?.errors) }}
        </span>
      </article>
      <article class="metric-tile tone-tokens">
        <span class="metric-label">Token 消耗</span>
        <div class="metric-value">
          {{ formatNumber(summary?.total_tokens) }}<span class="metric-unit">Tokens</span>
        </div>
        <span class="metric-hint">
          输入 {{ formatNumber(summary?.prompt_tokens) }} · 输出
          {{ formatNumber(summary?.completion_tokens) }}
        </span>
      </article>
      <article class="metric-tile tone-errors">
        <span class="metric-label">错误率</span>
        <div class="metric-value" :class="{ 'is-warning': errorRate > 5 }">
          {{ errorRateText }}
        </div>
        <span class="metric-hint">
          P95 {{ formatNumber(summary?.p95_latency_ms) }} ms · DLP 命中
          {{ formatNumber(summary?.dlp_hits) }} 次
        </span>
      </article>
    </div>

    <el-card class="trend-card" shadow="never">
      <div class="card-heading">
        <div>
          <div class="card-title-row">
            <h3>用量趋势</h3>
            <span class="card-total">{{ metricTotal }} {{ metricLabel }}</span>
          </div>
          <p>{{ rangeLabel }} · 按日聚合（北京时间）</p>
        </div>
        <el-radio-group
          :model-value="metric"
          size="small"
          @change="onMetricChange"
        >
          <el-radio-button
            v-for="option in metricOptions"
            :key="option.value"
            :value="option.value"
          >
            {{ option.label }}
          </el-radio-button>
        </el-radio-group>
      </div>

      <UsageTrendChart
        v-if="hasTraffic"
        :days="days"
        :requests="requestSeries"
        :tokens="tokenSeries"
        :metric="metric"
      />
      <el-empty
        v-else
        description="所选周期内暂无调用记录"
        :image-size="72"
        class="trend-empty"
      />

      <div class="card-foot">
        <el-button text type="primary" @click="$router.push('/portal/usage')">
          查看用量明细
          <el-icon class="link-icon"><ArrowRight /></el-icon>
        </el-button>
      </div>
    </el-card>

    <section v-if="models.length > 0" class="models-section">
      <div class="section-heading">
        <div>
          <h3>按模型</h3>
          <span>按 Token 消耗排序</span>
        </div>
      </div>
      <div class="model-grid">
        <ModelUsageCard
          v-for="model in displayedModels"
          :key="model.model"
          :model="model"
          :days="days"
          :metric="metric"
          :share="shareOfTokens(model.tokens)"
          :range-label="rangeLabel"
        />
      </div>
      <p v-if="models.length > displayedModels.length" class="models-more">
        另有 {{ models.length - displayedModels.length }} 个模型未显示，可在「用量明细」中查看。
      </p>
    </section>

  </div>
</template>

<style scoped>
.page-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 18px;
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

.filter-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
  padding: 10px 14px;
  border: 1px solid var(--gw-border);
  border-radius: 14px;
  background: #fff;
  box-shadow: var(--gw-shadow-card);
}

.filter-group {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.range-picker {
  width: 248px;
}

.range-picker :deep(.el-range-separator) {
  padding: 0 4px;
  font-size: 12px;
}

.filter-label {
  color: #7b8a9e;
  font-size: 12px;
  font-weight: 600;
}

.filter-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.updated-at {
  color: #9aa7b8;
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}

.filter-bar :deep(.el-radio-group) {
  padding: 3px;
  border-radius: 9px;
  background: #edf1f7;
}

.filter-bar :deep(.el-radio-button__inner) {
  border: none;
  border-radius: 7px !important;
  background: transparent;
  box-shadow: none;
  color: var(--gw-text-secondary);
  font-size: 12px;
  padding: 6px 14px;
}

.filter-bar :deep(.el-radio-button.is-active .el-radio-button__inner) {
  background: #fff;
  box-shadow: 0 1px 3px rgba(23, 32, 51, 0.12);
  color: var(--gw-primary);
}

.load-error {
  margin-bottom: 16px;
}

.quota-card {
  margin-bottom: 18px;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.quota-card :deep(.el-card__body) {
  display: flex;
  align-items: center;
  gap: 26px;
  padding: 18px 22px;
}

.quota-block {
  flex: 1;
  min-width: 0;
}

.quota-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 4px;
}

.quota-label {
  color: #7b8a9e;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
}

.quota-value {
  margin-bottom: 8px;
  color: var(--gw-text);
  font-size: 25px;
  font-weight: 650;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
  line-height: 1.15;
}

.quota-unit {
  margin-left: 7px;
  color: #93a0b2;
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0;
}

.quota-meta {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 6px 16px;
  margin-top: 7px;
  color: #93a0b2;
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}

.quota-side {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 8px;
  flex-shrink: 0;
  padding-left: 24px;
  border-left: 1px solid var(--gw-border);
}

.quota-keys {
  color: #7b8a9e;
  font-size: 11px;
}

.quota-links {
  display: flex;
  align-items: center;
  gap: 6px;
}

.quota-links :deep(.el-button + .el-button) {
  margin-left: 0;
}

.metrics {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 14px;
  margin-bottom: 18px;
}

.metric-tile {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 8px;
  overflow: hidden;
  padding: 18px 20px;
  border: 1px solid var(--gw-border);
  border-radius: 16px;
  background: #fff;
  box-shadow: var(--gw-shadow-card);
  transition:
    transform 0.18s ease,
    box-shadow 0.18s ease;
}

.metric-tile:hover {
  transform: translateY(-2px);
  box-shadow:
    0 1px 2px rgba(23, 32, 51, 0.04),
    0 18px 36px -22px rgba(23, 32, 51, 0.28);
}

.metric-tile::before {
  position: absolute;
  top: 0;
  right: 18px;
  left: 18px;
  height: 2px;
  border-radius: 0 0 2px 2px;
  background: var(--tile-accent, var(--gw-primary));
  opacity: 0.7;
  content: '';
}

.metric-tile.tone-requests {
  --tile-accent: #2f6bff;
}

.metric-tile.tone-tokens {
  --tile-accent: #f08a4b;
}

.metric-tile.tone-errors {
  --tile-accent: #d08a17;
}

.metric-label {
  color: #7b8a9e;
  font-size: 12px;
  font-weight: 600;
}

.metric-value {
  color: var(--gw-text);
  font-size: 30px;
  font-weight: 650;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
  line-height: 1.1;
}

.metric-value.is-warning {
  color: var(--gw-warning);
}

.metric-unit {
  margin-left: 6px;
  color: #9aa7b8;
  font-size: 12px;
  font-weight: 500;
  letter-spacing: 0;
}

.metric-hint {
  color: #93a0b2;
  font-size: 11px;
  line-height: 1.45;
}

.trend-card {
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.trend-card :deep(.el-card__body) {
  padding: 20px 22px 12px;
}

.card-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 8px;
}

.card-title-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
}

.card-heading h3 {
  margin: 0;
  color: var(--gw-text);
  font-size: 15px;
  font-weight: 650;
}

.card-total {
  color: #33415c;
  font-size: 13px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.card-heading p {
  margin: 4px 0 0;
  color: var(--gw-text-muted);
  font-size: 11px;
}

.card-heading :deep(.el-radio-group) {
  padding: 3px;
  border-radius: 9px;
  background: #f4f7fb;
}

.card-heading :deep(.el-radio-button__inner) {
  border: none;
  border-radius: 7px !important;
  background: transparent;
  box-shadow: none;
  color: var(--gw-text-secondary);
  font-size: 12px;
  padding: 6px 12px;
}

.card-heading :deep(.el-radio-button.is-active .el-radio-button__inner) {
  background: #fff;
  box-shadow: 0 1px 3px rgba(23, 32, 51, 0.12);
  color: var(--gw-primary);
}

.trend-empty {
  padding: 40px 0;
}

.card-foot {
  display: flex;
  justify-content: flex-end;
  padding-top: 4px;
}

.link-icon {
  margin-left: 3px;
}

.models-section {
  margin-top: 22px;
}

.section-heading {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  margin-bottom: 12px;
}

.section-heading h3 {
  margin: 0 0 3px;
  color: var(--gw-text);
  font-size: 15px;
  font-weight: 650;
}

.section-heading span {
  color: var(--gw-text-muted);
  font-size: 11px;
}

.model-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}

.models-more {
  margin: 10px 0 0;
  color: #9aa7b8;
  font-size: 11px;
}

@media (max-width: 1000px) {
  .metrics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 900px) {
  .model-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .filter-bar {
    flex-direction: column;
    align-items: stretch;
  }

  .filter-actions {
    justify-content: space-between;
  }

  .quota-card :deep(.el-card__body) {
    flex-direction: column;
    align-items: stretch;
    gap: 14px;
  }

  .quota-side {
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
    padding-top: 12px;
    padding-left: 0;
    border-top: 1px solid var(--gw-border);
    border-left: none;
  }

  .metrics {
    grid-template-columns: 1fr;
  }

  .card-heading {
    flex-direction: column;
  }
}
</style>
