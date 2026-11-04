<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsCoreOption } from 'echarts/core'

import type { PortalModelUsage, PortalTrendMetric } from '../../api/types'
import { formatNumber } from '../../utils/format'

const props = defineProps<{
  model: PortalModelUsage
  days: string[]
  metric: PortalTrendMetric
  share: number
  rangeLabel: string
}>()

const chartEl = ref<HTMLDivElement | null>(null)
let chart: ReturnType<typeof echarts.init> | null = null
let observer: ResizeObserver | null = null

echarts.use([LineChart, GridComponent, TooltipComponent, CanvasRenderer])

const series = computed<number[]>(() => {
  const byDay = new Map(props.model.trend.map((point) => [point.day, point]))
  return props.days.map((day) => {
    const point = byDay.get(day)
    if (!point) return 0
    return props.metric === 'requests' ? point.requests : point.tokens
  })
})

const metricLabel = computed(() =>
  props.metric === 'requests' ? '请求次数' : 'Tokens',
)

const shareText = computed(() => {
  if (!Number.isFinite(props.share) || props.share <= 0) return '—'
  const pct = props.share * 100
  return `${pct >= 10 ? pct.toFixed(0) : pct.toFixed(1)}%`
})

function compact(value: number): string {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return String(Math.round(value))
}

type TooltipParam = { dataIndex: number }

function tooltipFormatter(params: TooltipParam[] | TooltipParam): string {
  const first = Array.isArray(params) ? params[0] : params
  const index = first?.dataIndex ?? 0
  const point = props.model.trend.find((p) => p.day === props.days[index])
  const day = props.days[index] ?? ''
  return [
    `<div style="font-weight:600;margin-bottom:4px">${day}</div>`,
    `请求次数：${formatNumber(point?.requests ?? 0)}`,
    `Tokens：${formatNumber(point?.tokens ?? 0)}`,
  ].join('<br/>')
}

function renderChart(): void {
  if (!chartEl.value) return
  if (!chart) chart = echarts.init(chartEl.value)

  const color = props.metric === 'tokens' ? '#f08a4b' : '#2f6bff'
  const option: EChartsCoreOption = {
    grid: { left: 46, right: 12, top: 14, bottom: 26 },
    tooltip: {
      trigger: 'axis',
      backgroundColor: 'rgba(255, 255, 255, 0.98)',
      borderColor: '#e5ebf3',
      borderWidth: 1,
      padding: [8, 10],
      textStyle: { color: '#33415c', fontSize: 12 },
      extraCssText:
        'border-radius: 10px; box-shadow: 0 12px 30px -12px rgba(23, 32, 51, 0.28);',
      axisPointer: { type: 'line', lineStyle: { color: '#c9d4e2' } },
      formatter: tooltipFormatter,
    },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: props.days.map((day) => day.slice(5)),
      axisLine: { lineStyle: { color: '#e5ebf3' } },
      axisLabel: { color: '#9aa7b8', fontSize: 10, hideOverlap: true },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      min: 0,
      minInterval: 1,
      axisLabel: { color: '#9aa7b8', fontSize: 10, formatter: compact },
      splitLine: { lineStyle: { color: '#eef2f7' } },
    },
    series: [
      {
        type: 'line',
        data: series.value,
        smooth: true,
        showSymbol: series.value.length <= 2,
        lineStyle: { width: 2, color },
        itemStyle: { color },
        areaStyle: {
          color: {
            type: 'linear',
            x: 0,
            y: 0,
            x2: 0,
            y2: 1,
            colorStops: [
              { offset: 0, color: props.metric === 'tokens' ? 'rgba(240, 138, 75, 0.28)' : 'rgba(47, 107, 255, 0.24)' },
              { offset: 1, color: 'rgba(255, 255, 255, 0)' },
            ],
          },
        },
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
  () => [props.model, props.days, props.metric],
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
  <article class="model-card">
    <header class="model-head">
      <div class="model-title">
        <span class="model-dot" :class="metric" />
        <strong>{{ model.model }}</strong>
      </div>
      <span class="model-share">Token 占比 {{ shareText }}</span>
    </header>

    <div class="model-stats">
      <div class="stat">
        <strong>{{ formatNumber(model.requests) }}</strong>
        <span>请求次数</span>
      </div>
      <div class="stat">
        <strong>{{ formatNumber(model.tokens) }}</strong>
        <span>Tokens</span>
      </div>
      <div class="stat" :class="{ 'has-error': model.errors > 0 }">
        <strong>{{ formatNumber(model.errors) }}</strong>
        <span>失败请求</span>
      </div>
    </div>

    <div ref="chartEl" class="model-chart" />

    <p class="model-foot">{{ rangeLabel }} · {{ metricLabel }}趋势</p>
  </article>
</template>

<style scoped>
.model-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px 18px 12px;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  background: #fff;
  box-shadow: var(--gw-shadow-card);
}

.model-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.model-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.model-title strong {
  overflow: hidden;
  color: var(--gw-text);
  font-size: 14px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-dot {
  flex-shrink: 0;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #2f6bff;
}

.model-dot.tokens {
  background: #f08a4b;
}

.model-share {
  flex-shrink: 0;
  padding: 3px 8px;
  border-radius: 999px;
  background: #f4f7fb;
  color: #7b8a9e;
  font-size: 10px;
  font-weight: 600;
}

.model-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
}

.stat {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.stat strong {
  color: var(--gw-text);
  font-size: 17px;
  font-weight: 650;
  letter-spacing: -0.01em;
  font-variant-numeric: tabular-nums;
}

.stat span {
  color: #9aa7b8;
  font-size: 10px;
}

.stat.has-error strong {
  color: var(--gw-danger);
}

.model-chart {
  width: 100%;
  height: 136px;
}

.model-foot {
  margin: 0;
  color: #9aa7b8;
  font-size: 10px;
}
</style>
