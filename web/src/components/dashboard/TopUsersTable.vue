<script setup lang="ts">
import type { DashboardQuotaBrief, DashboardUser } from '../../api/types'
import {
  formatNumber,
  formatPercent,
  quotaProgressStatus,
} from '../../utils/format'

defineProps<{
  users: DashboardUser[]
}>()

const emit = defineEmits<{
  'select-user': [employeeId: string]
  'view-tokens': [employeeId: string]
}>()

function quotaPeriodLabel(quota: DashboardQuotaBrief): string {
  if (quota.period === 'daily') return '每日'
  if (quota.period === 'monthly') return '每月'
  return '总量'
}

function quotaTypeLabel(quota: DashboardQuotaBrief): string {
  return quota.type === 'tokens' ? 'Token' : '请求数'
}

function employeeLabel(user: DashboardUser): string {
  return user.name || user.employee_id.slice(0, 8)
}

function employeeInitial(user: DashboardUser): string {
  return employeeLabel(user).slice(0, 1).toUpperCase()
}
</script>

<template>
  <el-card class="users-card" shadow="never">
    <div class="card-heading">
      <div>
        <h3>员工用量 Top 10</h3>
        <span>按 Token 消耗排序，可下钻到审计日志或 Token 管理</span>
      </div>
    </div>

    <el-table :data="users" size="small" class="users-table">
      <el-table-column type="index" label="#" width="44" />
      <el-table-column label="员工" min-width="150">
        <template #default="{ row }">
          <div class="employee">
            <span class="mini-avatar">{{ employeeInitial(row) }}</span>
            <div class="employee-text">
              <strong>{{ employeeLabel(row) }}</strong>
              <span>
                {{ row.employee_no || '—' }} · {{ row.department || '未分组' }}
              </span>
            </div>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="请求" width="84" align="right">
        <template #default="{ row }">
          {{ formatNumber(row.requests) }}
        </template>
      </el-table-column>
      <el-table-column label="Token" width="104" align="right">
        <template #default="{ row }">
          {{ formatNumber(row.tokens) }}
        </template>
      </el-table-column>
      <el-table-column label="DLP" width="70" align="right">
        <template #default="{ row }">
          <el-tag v-if="row.dlp_requests > 0" type="warning" size="small">
            {{ formatNumber(row.dlp_requests) }}
          </el-tag>
          <span v-else class="muted">0</span>
        </template>
      </el-table-column>
      <el-table-column label="配额使用" min-width="170">
        <template #default="{ row }">
          <div v-if="row.quota" class="quota">
            <div class="quota-head">
              <span>
                {{ formatPercent(row.quota.pct) }} ·
                {{ quotaPeriodLabel(row.quota) }}{{ quotaTypeLabel(row.quota) }}
              </span>
              <span class="muted">
                {{ formatNumber(row.quota.used) }}/{{ formatNumber(row.quota.total) }}
              </span>
            </div>
            <el-progress
              :percentage="Math.min(100, row.quota.pct * 100)"
              :status="quotaProgressStatus(row.quota.pct)"
              :stroke-width="6"
              :show-text="false"
            />
          </div>
          <span v-else class="muted">未设置</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="130" fixed="right">
        <template #default="{ row }">
          <el-button
            link
            type="primary"
            @click="emit('select-user', row.employee_id)"
          >
            审计
          </el-button>
          <el-button
            link
            type="primary"
            @click="emit('view-tokens', row.employee_id)"
          >
            Token
          </el-button>
        </template>
      </el-table-column>
      <template #empty>
        <el-empty description="所选范围内暂无员工调用" :image-size="60" />
      </template>
    </el-table>
  </el-card>
</template>

<style scoped>
.users-card {
  height: 100%;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  background: #fff;
  box-shadow: var(--gw-shadow-card);
}

.users-card :deep(.el-card__body) {
  padding: 20px 22px;
}

.card-heading {
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

.employee {
  display: flex;
  align-items: center;
  gap: 9px;
}

.mini-avatar {
  display: grid;
  flex-shrink: 0;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: #edf3ff;
  color: var(--gw-primary);
  font-size: 11px;
  font-weight: 700;
}

.employee-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.employee strong {
  overflow: hidden;
  color: #33415c;
  font-size: 12px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.employee-text span {
  color: var(--gw-text-muted);
  font-size: 10px;
}

.quota {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.quota-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  color: var(--gw-text-secondary);
  font-size: 10px;
  font-variant-numeric: tabular-nums;
}

.muted {
  color: var(--gw-text-muted);
  font-size: 11px;
}

.users-table :deep(.el-table__cell) {
  vertical-align: middle;
}
</style>
