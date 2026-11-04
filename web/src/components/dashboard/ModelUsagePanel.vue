<script setup lang="ts">
import { computed } from 'vue'

import type { DashboardModelStat } from '../../api/types'
import { formatNumber } from '../../utils/format'

const props = defineProps<{
  models: DashboardModelStat[]
  buckets: string[]
  totalTokens: number
}>()

const SPARK_WIDTH = 120
const SPARK_HEIGHT = 30

interface SparkGeometry {
  line: string
  area: string
}

function sparkGeometry(values: number[]): SparkGeometry {
  if (values.length === 0) return { line: '', area: '' }
  const max = Math.max(...values, 1)
  const yOf = (value: number): number =>
    SPARK_HEIGHT - 2 - (value / max) * (SPARK_HEIGHT - 4)
  const coords: Array<[number, number]> =
    values.length === 1
      ? [
          [0, yOf(values[0] ?? 0)],
          [SPARK_WIDTH, yOf(values[0] ?? 0)],
        ]
      : values.map((value, index) => [
          (index / (values.length - 1)) * SPARK_WIDTH,
          yOf(value),
        ])
  const line = coords
    .map(([x, y], index) => `${index === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`)
    .join(' ')
  const area = `${line} L${SPARK_WIDTH},${SPARK_HEIGHT} L0,${SPARK_HEIGHT} Z`
  return { line, area }
}

const rows = computed(() =>
  props.models.map((model) => {
    const byBucket = new Map(
      model.trend.map((point) => [point.bucket, point]),
    )
    const values = props.buckets.map(
      (bucket) => byBucket.get(bucket)?.requests ?? 0,
    )
    const label =
      model.model && model.model !== '未知' ? model.model : '未指定模型'
    const share = props.totalTokens > 0 ? model.tokens / props.totalTokens : 0
    return {
      ...model,
      label,
      share,
      shareText:
        share > 0
          ? `${share >= 0.1 ? (share * 100).toFixed(0) : (share * 100).toFixed(1)}%`
          : '—',
      spark: sparkGeometry(values),
    }
  }),
)
</script>

<template>
  <el-card class="model-panel" shadow="never">
    <div class="card-heading">
      <div>
        <h3>模型用量</h3>
        <span>按请求量排序 · 迷你趋势为请求量</span>
      </div>
      <span class="value-label">Token 占比</span>
    </div>

    <div v-if="models.length === 0" class="empty-row">
      <el-empty description="所选范围内无模型调用" :image-size="56" />
    </div>
    <ul v-else class="model-list">
      <li v-for="row in rows" :key="row.model" class="model-row">
        <div class="model-main">
          <div class="model-title">
            <span class="model-dot" />
            <strong :title="row.label">{{ row.label }}</strong>
          </div>
          <div class="model-meta">
            <span>请求 {{ formatNumber(row.requests) }}</span>
            <span>Token {{ formatNumber(row.tokens) }}</span>
            <span :class="{ 'has-error': row.errors > 0 }">
              错误 {{ formatNumber(row.errors) }}
            </span>
          </div>
        </div>
        <svg
          class="spark"
          viewBox="0 0 120 30"
          preserveAspectRatio="none"
          role="img"
          :aria-label="`${row.label} 请求量趋势`"
        >
          <path class="spark-area" :d="row.spark.area" />
          <path class="spark-line" :d="row.spark.line" />
        </svg>
        <span class="model-share">{{ row.shareText }}</span>
      </li>
    </ul>
  </el-card>
</template>

<style scoped>
.model-panel {
  height: 100%;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  background: #fff;
  box-shadow: var(--gw-shadow-card);
}

.model-panel :deep(.el-card__body) {
  padding: 20px 22px;
}

.card-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
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

.value-label {
  padding: 3px 8px;
  border-radius: 999px;
  background: #f4f7fb;
  color: #8694a7;
  font-size: 10px;
  white-space: nowrap;
}

.empty-row {
  padding: 12px 0;
}

.model-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.model-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 120px 44px;
  align-items: center;
  gap: 14px;
  padding: 8px 0;
  border-bottom: 1px solid #f1f4f9;
}

.model-row:last-child {
  border-bottom: none;
}

.model-main {
  min-width: 0;
}

.model-title {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}

.model-title strong {
  overflow: hidden;
  color: #33415c;
  font-size: 12px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-dot {
  flex-shrink: 0;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #2f6bff;
}

.model-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 3px;
  color: var(--gw-text-muted);
  font-size: 10px;
  font-variant-numeric: tabular-nums;
}

.model-meta .has-error {
  color: var(--gw-danger);
}

.spark {
  display: block;
  width: 120px;
  height: 30px;
  overflow: visible;
}

.spark-line {
  fill: none;
  stroke: #2f6bff;
  stroke-width: 1.6;
  stroke-linecap: round;
  stroke-linejoin: round;
  vector-effect: non-scaling-stroke;
}

.spark-area {
  fill: rgba(47, 107, 255, 0.12);
}

.model-share {
  color: #33415c;
  font-size: 12px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  text-align: right;
}

@media (max-width: 640px) {
  .model-row {
    grid-template-columns: minmax(0, 1fr) 70px 40px;
  }

  .spark {
    width: 70px;
  }
}
</style>
