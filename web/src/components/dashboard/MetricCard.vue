<script setup lang="ts">
import { computed } from 'vue'
import type { Component } from 'vue'

const props = withDefaults(
  defineProps<{
    label: string
    value: string
    hint?: string
    delta?: number | null
    deltaKind?: 'pct' | 'pp'
    direction?: 'up' | 'down' | 'none'
    icon?: Component
    tone?: 'primary' | 'success' | 'warning' | 'danger' | 'info'
  }>(),
  {
    hint: '',
    delta: undefined,
    deltaKind: 'pct',
    direction: 'none',
    icon: undefined,
    tone: 'primary',
  },
)

const deltaView = computed(() => {
  const delta = props.delta
  if (delta === null || delta === undefined) {
    return { text: '上期无数据', tone: 'neutral' }
  }
  if (Math.abs(delta) < 0.05) {
    return { text: '与上期持平', tone: 'neutral' }
  }
  const unit = props.deltaKind === 'pp' ? ' 个百分点' : '%'
  const arrow = delta > 0 ? '↑' : '↓'
  let tone = 'neutral'
  if (props.direction !== 'none') {
    const improved = props.direction === 'up' ? delta > 0 : delta < 0
    tone = improved ? 'good' : 'bad'
  }
  const suffix =
    props.direction === 'none'
      ? '较上期'
      : props.direction === 'up'
        ? delta > 0
          ? '较上期增加'
          : '较上期减少'
        : delta < 0
          ? '低于上期'
          : '高于上期'
  return { text: `${arrow} ${Math.abs(delta).toFixed(1)}${unit} · ${suffix}`, tone }
})
</script>

<template>
  <el-card class="metric-card" :class="`tone-${tone}`" shadow="never">
    <div class="metric-top">
      <span v-if="icon" class="metric-icon">
        <el-icon><component :is="icon" /></el-icon>
      </span>
      <span class="metric-label">{{ label }}</span>
    </div>
    <strong class="metric-value">{{ value }}</strong>
    <div class="metric-foot">
      <span class="metric-delta" :class="`is-${deltaView.tone}`">
        {{ deltaView.text }}
      </span>
      <span v-if="hint" class="metric-hint">{{ hint }}</span>
    </div>
  </el-card>
</template>

<style scoped>
.metric-card {
  position: relative;
  height: 100%;
  overflow: hidden;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  background: #fff;
  box-shadow: var(--gw-shadow-card);
  transition:
    transform 0.18s ease,
    box-shadow 0.18s ease;
}

.metric-card:hover {
  transform: translateY(-2px);
  box-shadow:
    0 1px 2px rgba(23, 32, 51, 0.04),
    0 18px 36px -22px rgba(23, 32, 51, 0.28);
}

.metric-card::before {
  position: absolute;
  top: 0;
  right: 18px;
  left: 18px;
  height: 2px;
  border-radius: 0 0 2px 2px;
  background: var(--metric-accent, var(--gw-primary));
  opacity: 0.7;
  content: '';
}

.metric-card.tone-success {
  --metric-accent: #1aa06d;
  --metric-soft: #eaf7f1;
}

.metric-card.tone-warning {
  --metric-accent: #d08a17;
  --metric-soft: #fdf5e8;
}

.metric-card.tone-danger {
  --metric-accent: #cf4a4a;
  --metric-soft: #fdf0f0;
}

.metric-card.tone-info {
  --metric-accent: #6b7a90;
  --metric-soft: #f1f4f8;
}

.metric-card.tone-primary {
  --metric-accent: #2f6bff;
  --metric-soft: #edf3ff;
}

.metric-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  gap: 9px;
  padding: 17px 18px 16px;
}

.metric-top {
  display: flex;
  align-items: center;
  gap: 9px;
}

.metric-icon {
  display: grid;
  flex-shrink: 0;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: var(--metric-soft, var(--gw-primary-soft));
  color: var(--metric-accent, var(--gw-primary));
  font-size: 15px;
}

.metric-label {
  color: var(--gw-text-secondary);
  font-size: 12px;
  font-weight: 500;
}

.metric-value {
  color: var(--gw-text);
  font-size: 27px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.02em;
  line-height: 1.1;
}

.metric-foot {
  display: flex;
  flex-direction: column;
  gap: 5px;
  margin-top: 1px;
}

.metric-delta {
  align-self: flex-start;
  padding: 2px 7px;
  border-radius: 999px;
  background: #f3f6fa;
  color: #7a8798;
  font-size: 10px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}

.metric-delta.is-good {
  background: #eaf7f1;
  color: #147a51;
}

.metric-delta.is-bad {
  background: #fdf0f0;
  color: #b43a3a;
}

.metric-hint {
  overflow: hidden;
  color: #9aa7b8;
  font-size: 11px;
  line-height: 1.4;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
