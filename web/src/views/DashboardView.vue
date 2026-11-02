<template>
  <div class="dashboard-page">
    <section class="page-heading">
      <h2>运行概览</h2>
      <p>集中查看 API 调用量、Token 消耗与数据安全事件。</p>
    </section>

    <el-row :gutter="18">
      <el-col v-for="card in cards" :key="card.label" :xs="24" :sm="12" :lg="6">
        <el-card class="metric-card" shadow="never">
          <span class="metric-label">{{ card.label }}</span>
          <strong class="metric-value">—</strong>
          <span class="metric-hint">{{ card.hint }}</span>
        </el-card>
      </el-col>
    </el-row>

    <el-card class="chart-card" shadow="never">
      <div class="chart-heading">
        <h3>调用趋势</h3>
        <span>即将接入实时统计数据</span>
      </div>
      <div class="chart-placeholder">
        <el-icon><DataLine /></el-icon>
        <p>统计数据准备中</p>
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { DataLine } from '@element-plus/icons-vue'

const cards = [
  { label: '今日调用', hint: '成功与失败请求总数' },
  { label: '活跃用户', hint: '今日产生调用的员工' },
  { label: 'Token 消耗', hint: '今日输入与输出合计' },
  { label: 'DLP 命中', hint: '今日敏感数据处置次数' },
]
</script>

<style scoped>
.page-heading {
  margin-bottom: 22px;
}

.page-heading h2 {
  margin: 0 0 8px;
  color: #1f2d3d;
  font-size: 22px;
}

.page-heading p {
  margin: 0;
  color: #8393a7;
  font-size: 14px;
}

.metric-card {
  margin-bottom: 18px;
  border: 1px solid #e8edf4;
  border-radius: 12px;
}

.metric-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 20px;
}

.metric-label {
  color: #7a8ba1;
  font-size: 13px;
}

.metric-value {
  color: #1f2d3d;
  font-size: 28px;
  line-height: 1;
}

.metric-hint {
  color: #a0adbd;
  font-size: 12px;
}

.chart-card {
  border: 1px solid #e8edf4;
  border-radius: 12px;
}

.chart-heading {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
}

.chart-heading h3 {
  margin: 0;
  color: #1f2d3d;
  font-size: 16px;
}

.chart-heading span {
  color: #9aa8b9;
  font-size: 12px;
}

.chart-placeholder {
  display: grid;
  place-items: center;
  gap: 8px;
  min-height: 260px;
  color: #b3bfcd;
}

.chart-placeholder .el-icon {
  font-size: 42px;
}

.chart-placeholder p {
  margin: 0;
  font-size: 13px;
}
</style>
