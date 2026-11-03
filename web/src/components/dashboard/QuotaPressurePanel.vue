<script setup lang="ts">
import type { DashboardQuotaPressure } from '../../api/types'
import { formatDateTime } from '../../utils/datetime'
import {
  formatNumber,
  formatPercent,
  quotaProgressStatus,
} from '../../utils/format'

defineProps<{
  items: DashboardQuotaPressure[]
}>()

const emit = defineEmits<{
  'view-tokens': [employeeId: string]
}>()

function periodLabel(item: DashboardQuotaPressure): string {
  const period =
    item.quota_period === 'daily'
      ? '每日'
      : item.quota_period === 'monthly'
        ? '每月'
        : '总量'
  const type = item.quota_type === 'tokens' ? 'Token' : '请求数'
  return `${period}${type}`
}
</script>

<template>
  <el-card class="quota-card" shadow="never">
    <div class="card-heading">
      <div>
        <h3>配额预警</h3>
        <span>已使用 ≥ 80% 的有效 Token，按使用率排序</span>
      </div>
      <el-tag v-if="items.length > 0" type="warning" size="small">
        {{ items.length }} 项
      </el-tag>
    </div>

    <div v-if="items.length === 0" class="empty-row">
      <el-empty description="暂无配额预警" :image-size="56" />
      <p class="empty-hint">所有有效 Token 的使用率均低于 80%。</p>
    </div>
    <ul v-else class="quota-list">
      <li v-for="item in items" :key="item.employee_id">
        <div class="quota-head">
          <div class="who">
            <strong>{{ item.name || '未知员工' }}</strong>
            <span>{{ item.department || '未分组' }} · {{ periodLabel(item) }}</span>
          </div>
          <strong class="pct">{{ formatPercent(item.pct) }}</strong>
        </div>
        <el-progress
          :percentage="Math.min(100, item.pct * 100)"
          :status="quotaProgressStatus(item.pct)"
          :stroke-width="7"
          :show-text="false"
        />
        <div class="quota-foot">
          <span>
            {{ formatNumber(item.used) }} / {{ formatNumber(item.total) }}
          </span>
          <span v-if="item.expires_at">
            到期 {{ formatDateTime(item.expires_at) }}
          </span>
          <el-button link type="primary" @click="emit('view-tokens', item.employee_id)">
            查看 Token
          </el-button>
        </div>
      </li>
    </ul>
  </el-card>
</template>

<style scoped>
.quota-card {
  height: 100%;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  background: #fff;
  box-shadow: var(--gw-shadow-card);
}

.quota-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: 20px 22px;
}

.card-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
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
  display: grid;
  flex: 1;
  place-items: center;
  padding: 12px 0 22px;
  text-align: center;
}

.empty-hint {
  margin: 0;
  color: var(--gw-text-muted);
  font-size: 11px;
}

.quota-list {
  display: flex;
  flex-direction: column;
  gap: 9px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.quota-list li {
  padding: 12px 13px;
  border: 1px solid #eef3f9;
  border-radius: 10px;
  background: #fbfcff;
}

.quota-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}

.who {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.who strong {
  color: #33415c;
  font-size: 12px;
  font-weight: 600;
}

.who span {
  color: var(--gw-text-muted);
  font-size: 10px;
}

.pct {
  color: #a46005;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.quota-foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin-top: 7px;
  color: var(--gw-text-muted);
  font-size: 10px;
  font-variant-numeric: tabular-nums;
}

.quota-foot .el-button {
  margin-left: auto;
  font-size: 11px;
}
</style>
