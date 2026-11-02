<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import {
  GridComponent,
  TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsCoreOption } from 'echarts/core'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  DLPStat,
  DashboardOverview,
  TopUser,
  TrendPoint,
  TrendResponse,
} from '../api/types'

echarts.use([LineChart, GridComponent, TooltipComponent, CanvasRenderer])

const rangeDays = ref(7)
const rangeOptions = [
  { label: '近 24 小时', value: 1 },
  { label: '近 7 天', value: 7 },
  { label: '近 30 天', value: 30 },
]

const overview = ref<DashboardOverview | null>(null)
const trendItems = ref<TrendPoint[]>([])
const topUsers = ref<TopUser[]>([])
const dlpStats = ref<DLPStat[]>([])
const loading = ref(false)
const loadError = ref('')

const chartEl = ref<HTMLDivElement | null>(null)
let chart: ReturnType<typeof echarts.init> | null = null

const numberFormat = new Intl.NumberFormat('zh-CN')

const cards = computed(() => [
  {
    label: '调用量',
    value: numberFormat.format(overview.value?.total_calls ?? 0),
    hint: `错误 ${numberFormat.format(overview.value?.error_calls ?? 0)} 次`,
  },
  {
    label: 'Token 消耗',
    value: numberFormat.format(overview.value?.total_tokens ?? 0),
    hint: '输入与输出合计',
  },
  {
    label: '活跃员工',
    value: numberFormat.format(overview.value?.active_users ?? 0),
    hint: '产生调用的员工数',
  },
  {
    label: 'DLP 命中',
    value: numberFormat.format(overview.value?.dlp_hits ?? 0),
    hint: `错误率 ${((overview.value?.error_rate ?? 0) * 100).toFixed(2)}%`,
  },
])

const actionLabels: Record<string, string> = {
  block: '阻断',
  mask: '脱敏',
  audit: '审计',
}

const actionTagTypes: Record<string, 'danger' | 'warning' | 'info'> = {
  block: 'danger',
  mask: 'warning',
  audit: 'info',
}

function rangeQuery(): string {
  const to = new Date()
  const from = new Date(to.getTime() - rangeDays.value * 24 * 60 * 60 * 1000)
  return `from=${encodeURIComponent(from.toISOString())}&to=${encodeURIComponent(to.toISOString())}`
}

async function loadDashboard(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const query = rangeQuery()
    const [overviewData, trendData, topData, dlpData] = await Promise.all([
      api.get<DashboardOverview>(`/dashboard/overview?${query}`),
      api.get<TrendResponse>(`/dashboard/trend?interval=day&${query}`),
      api.get<{ items: TopUser[] }>(
        `/dashboard/top-users?limit=10&${query}`,
      ),
      api.get<{ items: DLPStat[] }>(`/dashboard/dlp?${query}`),
    ])
    overview.value = overviewData
    trendItems.value = trendData.items
    topUsers.value = topData.items
    dlpStats.value = dlpData.items
    await nextTick()
    renderChart()
  } catch (error) {
    loadError.value = apiErrorMessage(error, '概览数据加载失败')
  } finally {
    loading.value = false
  }
}

function renderChart(): void {
  if (!chartEl.value) return
  if (!chart) {
    chart = echarts.init(chartEl.value)
  }
  const option: EChartsCoreOption = {
    color: ['#409eff'],
    tooltip: {
      trigger: 'axis',
    },
    grid: {
      left: 48,
      right: 24,
      top: 24,
      bottom: 32,
    },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: trendItems.value.map((point) => point.day.slice(5)),
      axisLine: { lineStyle: { color: '#dfe7f1' } },
      axisLabel: { color: '#8393a7' },
    },
    yAxis: {
      type: 'value',
      minInterval: 1,
      axisLabel: { color: '#8393a7' },
      splitLine: { lineStyle: { color: '#eef2f7' } },
    },
    series: [
      {
        name: '调用量',
        type: 'line',
        smooth: true,
        symbol: 'circle',
        symbolSize: 6,
        data: trendItems.value.map((point) => point.calls),
        lineStyle: { width: 2 },
        areaStyle: { opacity: 0.12 },
      },
    ],
  }
  chart.setOption(option, true)
}

function resizeChart(): void {
  chart?.resize()
}

function changeRange(value: number): void {
  rangeDays.value = value
  void loadDashboard()
}

function employeeLabel(user: TopUser): string {
  return user.name || user.employee_id.slice(0, 8)
}

function refresh(): void {
  void loadDashboard()
  ElMessage.success('正在刷新概览数据')
}

watch(trendItems, async () => {
  await nextTick()
  renderChart()
})

onMounted(async () => {
  await loadDashboard()
  window.addEventListener('resize', resizeChart)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', resizeChart)
  chart?.dispose()
  chart = null
})
</script>

<template>
  <div class="dashboard-page">
    <section class="page-heading">
      <div>
        <h2>运行概览</h2>
        <p>集中查看 API 调用量、Token 消耗与数据安全事件。</p>
      </div>
      <div class="heading-actions">
        <el-radio-group
          :model-value="rangeDays"
          size="small"
          @change="changeRange"
        >
          <el-radio-button
            v-for="option in rangeOptions"
            :key="option.value"
            :value="option.value"
          >
            {{ option.label }}
          </el-radio-button>
        </el-radio-group>
        <el-button :icon="Refresh" :loading="loading" @click="refresh">
          刷新
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

    <el-row :gutter="18">
      <el-col v-for="card in cards" :key="card.label" :xs="24" :sm="12" :lg="6">
        <el-card class="metric-card" shadow="never">
          <span class="metric-label">{{ card.label }}</span>
          <strong class="metric-value">{{ card.value }}</strong>
          <span class="metric-hint">{{ card.hint }}</span>
        </el-card>
      </el-col>
    </el-row>

    <el-card v-loading="loading" class="chart-card" shadow="never">
      <div class="card-heading">
        <div>
          <h3>调用趋势</h3>
          <span>按北京时间自然日统计</span>
        </div>
      </div>
      <div ref="chartEl" class="chart-canvas" />
      <el-empty
        v-if="trendItems.length === 0"
        description="所选时间范围内暂无调用数据"
        :image-size="70"
        class="chart-empty"
      />
    </el-card>

    <el-row :gutter="18" class="lower-row">
      <el-col :xs="24" :lg="14">
        <el-card class="list-card" shadow="never">
          <div class="card-heading">
            <h3>调用量 Top 员工</h3>
          </div>
          <el-table :data="topUsers" size="small">
            <el-table-column type="index" label="#" width="48" />
            <el-table-column label="员工" min-width="140">
              <template #default="{ row }">{{ employeeLabel(row) }}</template>
            </el-table-column>
            <el-table-column label="调用量" width="110" align="right">
              <template #default="{ row }">
                {{ numberFormat.format(row.calls) }}
              </template>
            </el-table-column>
            <el-table-column label="Token" width="140" align="right">
              <template #default="{ row }">
                {{ numberFormat.format(row.tokens) }}
              </template>
            </el-table-column>
            <template #empty>
              <el-empty description="暂无调用数据" :image-size="60" />
            </template>
          </el-table>
        </el-card>
      </el-col>

      <el-col :xs="24" :lg="10">
        <el-card class="list-card" shadow="never">
          <div class="card-heading">
            <h3>DLP 规则命中</h3>
          </div>
          <el-table :data="dlpStats" size="small">
            <el-table-column prop="rule_name" label="规则" min-width="150" />
            <el-table-column label="动作" width="90">
              <template #default="{ row }">
                <el-tag
                  :type="actionTagTypes[row.action] ?? 'info'"
                  size="small"
                >
                  {{ actionLabels[row.action] ?? row.action }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="命中" width="90" align="right">
              <template #default="{ row }">
                {{ numberFormat.format(row.hits) }}
              </template>
            </el-table-column>
            <template #empty>
              <el-empty description="暂无 DLP 命中" :image-size="60" />
            </template>
          </el-table>
        </el-card>
      </el-col>
    </el-row>
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

.heading-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.load-error {
  margin-bottom: 18px;
}

.metric-card {
  margin-bottom: 18px;
  border: 1px solid #e8edf4;
  border-radius: 12px;
}

.metric-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 20px;
}

.metric-label {
  color: #7a8ba1;
  font-size: 13px;
}

.metric-value {
  color: #1f2d3d;
  font-size: 28px;
  line-height: 1;
}

.metric-hint {
  color: #a0adbd;
  font-size: 12px;
}

.chart-card,
.list-card {
  border: 1px solid #e8edf4;
  border-radius: 12px;
}

.card-heading {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 12px;
}

.card-heading h3 {
  margin: 0;
  color: #1f2d3d;
  font-size: 16px;
}

.card-heading span {
  color: #9aa8b9;
  font-size: 12px;
}

.chart-canvas {
  height: 300px;
}

.chart-empty {
  margin-top: -210px;
  pointer-events: none;
}

.lower-row {
  margin-top: 18px;
}

@media (max-width: 900px) {
  .page-heading {
    flex-direction: column;
  }

  .heading-actions {
    flex-wrap: wrap;
  }

  .list-card {
    margin-bottom: 18px;
  }
}
</style>
