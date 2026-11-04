<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsCoreOption } from 'echarts/core'

import type { PortalTrendMetric } from '../../api/types'
import { formatNumber } from '../../utils/format'

echarts.use([BarChart, GridComponent, TooltipComponent, CanvasRenderer])

const props = defineProps<{
  days: string[]
  requests: number[]
  tokens: number[]
  metric: PortalTrendMetric
}>()

const chartEl = ref<HTMLDivElement | null>(null)
let chart: ReturnType<typeof echarts.init> | null = null
let observer: ResizeObserver | null = null

function compact(value: number): string {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return String(Math.round(value))
}

function gradient(top: string, bottom: string): Record<string, unknown> {
  return {
    type: 'linear',
    x: 0,
    y: 0,
    x2: 0,
    y2: 1,
    colorStops: [
      { offset: 0, color: top },
      { offset: 1, color: bottom },
    ],
  }
}

function seriesColor(metric: PortalTrendMetric): Record<string, unknown> {
  return metric === 'tokens'
    ? gradient('#f5a623', '#f08a4b')
    : gradient('#5b8cff', '#2f6bff')
}

type TooltipParam = { dataIndex: number }

function tooltipFormatter(params: TooltipParam[] | TooltipParam): string {
  const first = Array.isArray(params) ? params[0] : params
  const index = first?.dataIndex ?? 0
  const day = props.days[index] ?? ''
  return [
    `<div style="font-weight:600;margin-bottom:4px">${day}</div>`,
    `请求次数：${formatNumber(props.requests[index])}`,
    `Tokens：${formatNumber(props.tokens[index])}`,
  ].join('<br/>')
}

function renderChart(): void {
  if (!chartEl.value) return
  if (!chart) chart = echarts.init(chartEl.value)

  const values = props.metric === 'requests' ? props.requests : props.tokens
  const option: EChartsCoreOption = {
    grid: { left: 52, right: 18, top: 18, bottom: 32 },
    tooltip: {
      trigger: 'axis',
      backgroundColor: 'rgba(255, 255, 255, 0.98)',
      borderColor: '#e5ebf3',
      borderWidth: 1,
      padding: [9, 12],
      textStyle: { color: '#33415c', fontSize: 12 },
      extraCssText:
        'border-radius: 10px; box-shadow: 0 12px 30px -12px rgba(23, 32, 51, 0.28);',
      axisPointer: { type: 'shadow', shadowStyle: { color: 'rgba(47, 107, 255, 0.06)' } },
      formatter: tooltipFormatter,
    },
    xAxis: {
      type: 'category',
      data: props.days.map((day) => day.slice(5)),
      axisLine: { lineStyle: { color: '#e5ebf3' } },
      axisLabel: { color: '#8a97a8', fontSize: 11, hideOverlap: true },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      min: 0,
      minInterval: 1,
      axisLabel: { color: '#8a97a8', fontSize: 11, formatter: compact },
      splitLine: { lineStyle: { color: '#eef2f7' } },
    },
    series: [
      {
        name: props.metric === 'requests' ? '请求次数' : 'Tokens',
        type: 'bar',
        data: values,
        barMaxWidth: 26,
        itemStyle: {
          color: seriesColor(props.metric),
          borderRadius: [6, 6, 2, 2],
        },
        emphasis: { itemStyle: { opacity: 0.85 } },
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
  () => [props.days, props.requests, props.tokens, props.metric],
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
  <div ref="chartEl" class="usage-trend-chart" />
</template>

<style scoped>
.usage-trend-chart {
  width: 100%;
  height: 300px;
}
</style>
