<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsCoreOption } from 'echarts/core'

import type { DashboardTrend } from '../../api/types'
import {
  formatBucketLabel,
  formatDuration,
  formatNumber,
} from '../../utils/format'

echarts.use([
  LineChart,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  CanvasRenderer,
])

export type TrendMetric = 'requests' | 'tokens' | 'errors' | 'latency'

const props = defineProps<{
  trend: DashboardTrend
  metric: TrendMetric
}>()

const emit = defineEmits<{
  'update:metric': [value: TrendMetric]
}>()

const metricOptions: Array<{ label: string; value: TrendMetric }> = [
  { label: '调用量', value: 'requests' },
  { label: 'Token', value: 'tokens' },
  { label: '错误请求', value: 'errors' },
  { label: 'P95 延迟', value: 'latency' },
]

const chartEl = ref<HTMLDivElement | null>(null)
let chart: ReturnType<typeof echarts.init> | null = null
let observer: ResizeObserver | null = null

const metricLabel = computed(
  () =>
    metricOptions.find((option) => option.value === props.metric)?.label ??
    '调用量',
)

function valueOf(
  point: DashboardTrend['current'][number] | undefined,
  metric: TrendMetric,
): number | null {
  if (!point) return null
  if (metric === 'latency') return point.p95_latency_ms
  return point[metric]
}

function onMetricChange(value: string | number | boolean | undefined): void {
  emit('update:metric', value as TrendMetric)
}

const hasData = computed(() =>
  props.trend.current.some((point) => (valueOf(point, props.metric) ?? 0) > 0),
)

function axisLabel(value: number): string {
  if (props.metric === 'latency') return `${formatNumber(value)} ms`
  return formatNumber(value)
}

function tooltipValue(value: number | null | undefined): string {
  if (value === null || value === undefined) return '样本不足'
  return props.metric === 'latency'
    ? formatDuration(value)
    : formatNumber(value)
}

function renderChart(): void {
  if (!chartEl.value) return
  if (!chart) chart = echarts.init(chartEl.value)

  const buckets = props.trend.buckets.map((bucket) =>
    formatBucketLabel(bucket, props.trend.interval),
  )
  const current = props.trend.current.map((point) =>
    valueOf(point, props.metric),
  )
  const previous = props.trend.previous.map((point) =>
    valueOf(point, props.metric),
  )

  const option: EChartsCoreOption = {
    color: ['#2F6BFF', '#C26A00'],
    legend: {
      top: 0,
      right: 8,
      itemWidth: 12,
      itemHeight: 8,
      textStyle: { color: '#5c6b80', fontSize: 11 },
    },
    tooltip: {
      trigger: 'axis',
      backgroundColor: 'rgba(255, 255, 255, 0.98)',
      borderColor: '#e5ebf3',
      borderWidth: 1,
      padding: [9, 12],
      textStyle: { color: '#33415c', fontSize: 12 },
      extraCssText:
        'border-radius: 10px; box-shadow: 0 12px 30px -12px rgba(23, 32, 51, 0.28);',
      axisPointer: { type: 'line', lineStyle: { color: '#c9d4e2' } },
      valueFormatter: (value: unknown) =>
        tooltipValue(value as number | null),
    },
    grid: { left: 54, right: 24, top: 42, bottom: 34 },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: buckets,
      axisLine: { lineStyle: { color: '#e5ebf3' } },
      axisLabel: { color: '#8a97a8', fontSize: 11, hideOverlap: true },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      min: 0,
      minInterval: props.metric === 'latency' ? 0 : 1,
      axisLabel: { color: '#8a97a8', fontSize: 11, formatter: axisLabel },
      splitLine: { lineStyle: { color: '#eef2f7' } },
    },
    series: [
      {
        name: `本期${metricLabel.value}`,
        type: 'line',
        data: current,
        connectNulls: false,
        symbol: 'circle',
        symbolSize: 8,
        showSymbol: buckets.length <= 24,
        lineStyle: { width: 2 },
        itemStyle: { borderColor: '#ffffff', borderWidth: 2 },
        areaStyle: { opacity: 0.1 },
        emphasis: { focus: 'series' },
      },
      {
        name: '上一周期',
        type: 'line',
        data: previous,
        connectNulls: false,
        symbol: 'circle',
        symbolSize: 8,
        showSymbol: false,
        lineStyle: { width: 2, type: 'dashed' },
        itemStyle: { borderColor: '#ffffff', borderWidth: 2 },
        emphasis: { focus: 'series' },
      },
    ],
  }
  chart.setOption(option, true)
}

async function redraw(): Promise<void> {
  await nextTick()
  renderChart()
}

watch(
  () => [props.trend, props.metric],
  () => {
    void redraw()
  },
  { deep: true },
)

onMounted(() => {
  renderChart()
  if (chartEl.value && typeof ResizeObserver !== 'undefined') {
    observer = new ResizeObserver(() => chart?.resize())
    observer.observe(chartEl.value)
  }
})

onBeforeUnmount(() => {
  observer?.disconnect()
  observer = null
  chart?.dispose()
  chart = null
})
</script>

<template>
  <el-card class="trend-card" shadow="never">
    <div class="card-heading">
      <div>
        <h3>趋势</h3>
        <span>北京时间 · {{ trend.interval === 'hour' ? '按小时' : '按日' }}聚合</span>
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

    <div class="chart-wrap">
      <div ref="chartEl" class="chart-canvas" />
      <el-empty
        v-if="!hasData"
        description="所选时间范围内暂无数据"
        :image-size="70"
        class="chart-empty"
      />
    </div>

    <details class="data-table">
      <summary>查看数据表</summary>
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>时间</th>
              <th>本期</th>
              <th>上一周期</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(bucket, index) in trend.buckets"
              :key="bucket"
            >
              <td>{{ formatBucketLabel(bucket, trend.interval) }}</td>
              <td>
                {{ tooltipValue(valueOf(trend.current[index], metric)) }}
              </td>
              <td>
                {{ tooltipValue(valueOf(trend.previous[index], metric)) }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>
  </el-card>
</template>

<style scoped>
.trend-card {
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  background: #fff;
  box-shadow: var(--gw-shadow-card);
}

.trend-card :deep(.el-card__body) {
  padding: 20px 22px 16px;
}

.card-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 10px;
}

.card-heading h3 {
  margin: 0 0 4px;
  color: var(--gw-text);
  font-size: 15px;
  font-weight: 600;
}

.card-heading span {
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

.chart-wrap {
  position: relative;
}

.chart-canvas {
  height: 310px;
}

.chart-empty {
  position: absolute;
  inset: 40% 0 auto;
  pointer-events: none;
}

.data-table {
  margin-top: 8px;
  color: var(--gw-text-muted);
  font-size: 11px;
}

.data-table summary {
  cursor: pointer;
}

.table-scroll {
  max-height: 220px;
  margin-top: 8px;
  overflow: auto;
}

table {
  width: 100%;
  border-collapse: collapse;
}

th,
td {
  padding: 6px 8px;
  border-bottom: 1px solid #eef2f7;
  text-align: left;
  font-variant-numeric: tabular-nums;
}

th {
  color: var(--gw-text-secondary);
  font-weight: 600;
}

@media (max-width: 700px) {
  .card-heading {
    flex-direction: column;
  }
}
</style>
