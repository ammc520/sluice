<script setup lang="ts">
import type { DashboardIncident, DLPAction } from '../../api/types'
import { formatDateTime } from '../../utils/datetime'

defineProps<{
  incidents: DashboardIncident[]
}>()

const emit = defineEmits<{
  'select-incident': [incident: DashboardIncident]
}>()

const actionLabels: Record<DLPAction, string> = {
  block: '阻断',
  mask: '脱敏',
  audit: '仅审计',
}

const actionTagTypes: Record<DLPAction, 'danger' | 'warning' | 'info'> = {
  block: 'danger',
  mask: 'warning',
  audit: 'info',
}
</script>

<template>
  <el-card class="incident-card" shadow="never">
    <div class="card-heading">
      <div>
        <h3>最近 DLP 事件</h3>
        <span>按发生时间倒序，最多显示 20 条</span>
      </div>
    </div>

    <div v-if="incidents.length === 0" class="empty-row">
      <el-empty description="暂无 DLP 事件" :image-size="56" />
    </div>
    <ul v-else class="incident-list">
      <li v-for="incident in incidents" :key="incident.id">
        <div class="incident-head">
          <div class="who">
            <strong>{{ incident.employee_name || '未知员工' }}</strong>
            <span>{{ formatDateTime(incident.event_time) }}</span>
          </div>
          <el-tag
            :type="actionTagTypes[incident.action] ?? 'info'"
            size="small"
          >
            {{ actionLabels[incident.action] ?? incident.action }}
          </el-tag>
        </div>
        <div class="rules">
          <span
            v-for="rule in incident.rules"
            :key="rule"
            class="rule-chip"
          >
            {{ rule }}
          </span>
          <span v-if="incident.rules.length === 0" class="muted">
            规则明细缺失
          </span>
        </div>
        <div class="incident-foot">
          <span class="model">{{ incident.model || '未知模型' }}</span>
          <el-button
            link
            type="primary"
            @click="emit('select-incident', incident)"
          >
            查看详情
          </el-button>
        </div>
      </li>
    </ul>
  </el-card>
</template>

<style scoped>
.incident-card {
  height: 100%;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  background: #fff;
  box-shadow: var(--gw-shadow-card);
}

.incident-card :deep(.el-card__body) {
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

.empty-row {
  padding: 8px 0;
}

.incident-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 390px;
  margin: 0;
  padding: 0;
  overflow: auto;
  list-style: none;
}

.incident-list li {
  padding: 11px 12px;
  border: 1px solid #eef3f9;
  border-radius: 10px;
  background: #fbfcff;
}

.incident-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
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
  font-variant-numeric: tabular-nums;
}

.rules {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
  margin-top: 7px;
}

.rule-chip {
  padding: 2px 7px;
  border-radius: 5px;
  background: #eef3fa;
  color: var(--gw-text-secondary);
  font-size: 10px;
}

.incident-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-top: 3px;
}

.model {
  color: var(--gw-text-muted);
  font-size: 10px;
}

.muted {
  color: var(--gw-text-muted);
  font-size: 10px;
}
</style>
