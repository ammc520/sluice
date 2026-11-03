<script setup lang="ts">
import { computed } from 'vue'

import { formatDuration, formatNumber, formatPercent } from '../../utils/format'

interface BreakdownRow {
  key: string
  label: string
  value: number
  tokens: number
  errors?: number
  latencyMs?: number
}

const props = defineProps<{
  title: string
  subtitle?: string
  rows: BreakdownRow[]
  emptyText: string
  valueLabel?: string
}>()

const maxValue = computed(() =>
  Math.max(1, ...props.rows.map((row) => row.value)),
)

function barWidth(value: number): string {
  return `${Math.max(2, (value / maxValue.value) * 100)}%`
}
</script>

<template>
  <el-card class="breakdown-card" shadow="never">
    <div class="card-heading">
      <div>
        <h3>{{ title }}</h3>
        <span v-if="subtitle">{{ subtitle }}</span>
      </div>
      <span class="value-label">{{ valueLabel ?? '请求量' }}</span>
    </div>

    <div v-if="rows.length === 0" class="empty-row">
      <el-empty :description="emptyText" :image-size="56" />
    </div>
    <ul v-else class="breakdown-list">
      <li v-for="row in rows" :key="row.key" class="breakdown-row">
        <div class="row-head">
          <span class="row-name" :title="row.label">{{ row.label }}</span>
          <strong class="row-value">{{ formatNumber(row.value) }}</strong>
        </div>
        <div
          class="bar-track"
          role="img"
          :aria-label="`${row.label}：${formatNumber(row.value)} 次请求`"
        >
          <span class="bar-fill" :style="{ width: barWidth(row.value) }" />
        </div>
        <div class="row-meta">
          <span>Token {{ formatNumber(row.tokens) }}</span>
          <span v-if="row.errors !== undefined">
            错误率 {{ formatPercent(row.value ? (row.errors ?? 0) / row.value : 0) }}
          </span>
          <span v-if="row.latencyMs !== undefined">
            P95 {{ formatDuration(row.latencyMs) }}
          </span>
        </div>
      </li>
    </ul>
  </el-card>
</template>

<style scoped>
.breakdown-card {
  height: 100%;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  background: #fff;
  box-shadow: var(--gw-shadow-card);
}

.breakdown-card :deep(.el-card__body) {
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

.breakdown-list {
  display: flex;
  flex-direction: column;
  gap: 13px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.breakdown-row {
  padding: 2px 0;
}

.row-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
}

.row-name {
  overflow: hidden;
  color: #33415c;
  font-size: 12px;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row-value {
  color: var(--gw-text);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.bar-track {
  height: 6px;
  margin-top: 6px;
  overflow: hidden;
  border-radius: 999px;
  background: #eef2f7;
}

.bar-fill {
  display: block;
  height: 100%;
  border-radius: 999px;
  background: linear-gradient(90deg, #2f6bff, #5b8cff);
}

.row-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 5px;
  color: var(--gw-text-muted);
  font-size: 10px;
  font-variant-numeric: tabular-nums;
}
</style>
