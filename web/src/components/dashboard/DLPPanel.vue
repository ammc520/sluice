<script setup lang="ts">
import { computed } from 'vue'
import type { Component } from 'vue'
import {
  CircleCloseFilled,
  Hide,
  View,
  WarningFilled,
} from '@element-plus/icons-vue'

import type { DashboardDLP, DLPAction } from '../../api/types'
import { formatNumber, formatPercent } from '../../utils/format'

const props = defineProps<{
  dlp: DashboardDLP
}>()

const actionMeta: Record<
  DLPAction,
  { label: string; tag: 'danger' | 'warning' | 'info'; color: string; icon: Component }
> = {
  block: {
    label: '阻断',
    tag: 'danger',
    color: '#c23b3b',
    icon: CircleCloseFilled,
  },
  mask: { label: '脱敏', tag: 'warning', color: '#b26a00', icon: Hide },
  audit: { label: '仅审计', tag: 'info', color: '#2f6bff', icon: View },
}

const actions = computed(() =>
  (['block', 'mask', 'audit'] as const).map((action) => {
    const item = props.dlp.by_action.find((entry) => entry.action === action)
    const requests = item?.requests ?? 0
    return {
      action,
      requests,
      hits: item?.hits ?? 0,
      share: props.dlp.requests ? requests / props.dlp.requests : 0,
      ...actionMeta[action],
    }
  }),
)

const hasHits = computed(() => props.dlp.requests > 0)
</script>

<template>
  <el-card class="dlp-card" shadow="never">
    <div class="card-heading">
      <div>
        <h3>DLP 风险</h3>
        <span>
          涉及 {{ formatNumber(dlp.requests) }} 个请求 ·
          命中 {{ formatNumber(dlp.hits) }} 次
        </span>
      </div>
      <el-tag v-if="hasHits" type="warning" effect="plain" size="small">
        <el-icon><WarningFilled /></el-icon>
        需关注
      </el-tag>
      <el-tag v-else type="success" effect="plain" size="small">无命中</el-tag>
    </div>

    <div v-if="!hasHits" class="empty-row">
      <el-empty description="所选范围内无敏感信息命中" :image-size="56" />
    </div>
    <template v-else>
      <ul class="action-list">
        <li v-for="item in actions" :key="item.action" class="action-row">
          <div class="action-head">
            <span class="action-name">
              <el-icon :style="{ color: item.color }">
                <component :is="item.icon" />
              </el-icon>
              {{ item.label }}
            </span>
            <span class="action-count">
              {{ formatNumber(item.requests) }} 请求
            </span>
          </div>
          <div
            class="bar-track"
            role="img"
            :aria-label="`${item.label}：${formatNumber(item.requests)} 个请求`"
          >
            <span
              class="bar-fill"
              :style="{ width: `${Math.max(item.share * 100, item.requests ? 2 : 0)}%`, background: item.color }"
            />
          </div>
          <span class="action-meta">
            占比 {{ formatPercent(item.share) }} · 命中
            {{ formatNumber(item.hits) }} 次
          </span>
        </li>
      </ul>

      <div class="rule-heading">
        <h4>高频规则</h4>
        <span>按命中次数排序</span>
      </div>
      <div v-if="dlp.by_rule.length === 0" class="muted">暂无规则明细</div>
      <ul v-else class="rule-list">
        <li v-for="rule in dlp.by_rule" :key="`${rule.rule_name}-${rule.action}`">
          <span class="rule-name" :title="rule.rule_name">{{ rule.rule_name }}</span>
          <el-tag :type="actionMeta[rule.action]?.tag ?? 'info'" size="small">
            {{ actionMeta[rule.action]?.label ?? rule.action }}
          </el-tag>
          <strong class="rule-hits">{{ formatNumber(rule.hits) }}</strong>
        </li>
      </ul>
    </template>
  </el-card>
</template>

<style scoped>
.dlp-card {
  height: 100%;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  background: #fff;
  box-shadow: var(--gw-shadow-card);
}

.dlp-card :deep(.el-card__body) {
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

.empty-row {
  padding: 8px 0;
}

.action-list,
.rule-list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.action-list {
  display: flex;
  flex-direction: column;
  gap: 13px;
}

.action-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.action-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: #33415c;
  font-size: 12px;
  font-weight: 500;
}

.action-count {
  color: var(--gw-text-secondary);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}

.bar-track {
  height: 6px;
  margin-top: 5px;
  overflow: hidden;
  border-radius: 999px;
  background: #eef2f7;
}

.bar-fill {
  display: block;
  height: 100%;
  border-radius: 999px;
}

.action-meta {
  display: block;
  margin-top: 4px;
  color: var(--gw-text-muted);
  font-size: 10px;
  font-variant-numeric: tabular-nums;
}

.rule-heading {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin: 18px 0 6px;
  padding-top: 14px;
  border-top: 1px solid #f0f4f9;
}

.rule-heading h4 {
  margin: 0;
  color: #33415c;
  font-size: 12px;
  font-weight: 600;
}

.rule-heading span {
  color: #a0adbd;
  font-size: 10px;
}

.rule-list {
  max-height: 196px;
  overflow: auto;
}

.rule-list li {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto 48px;
  align-items: center;
  gap: 10px;
  padding: 7px 0;
  border-bottom: 1px solid #f1f5f9;
}

.rule-list li:last-child {
  border-bottom: none;
}

.rule-name {
  overflow: hidden;
  color: var(--gw-text-secondary);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rule-hits {
  color: var(--gw-text);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
  text-align: right;
}

.muted {
  color: var(--gw-text-muted);
  font-size: 11px;
}
</style>
