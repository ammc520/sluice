<script setup lang="ts">
import { Tools } from '@element-plus/icons-vue'

defineProps<{
  title: string
  description?: string
}>()
</script>

<template>
  <div class="placeholder-page">
    <section class="page-heading">
      <h2>{{ title }}</h2>
      <p v-if="description">{{ description }}</p>
    </section>

    <el-card class="placeholder-card" shadow="never">
      <el-icon><Tools /></el-icon>
      <p>页面功能准备中</p>
    </el-card>
  </div>
</template>

<style scoped>
.page-heading {
  margin-bottom: 20px;
}

.page-heading h2 {
  margin: 0 0 7px;
  color: var(--gw-text);
  font-size: 23px;
  font-weight: 650;
  letter-spacing: -0.02em;
}

.page-heading p {
  margin: 0;
  color: var(--gw-text-muted);
  font-size: 13px;
}

.placeholder-card {
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.placeholder-card :deep(.el-card__body) {
  display: grid;
  place-items: center;
  gap: 10px;
  min-height: 320px;
  color: #a7b4c4;
}

.placeholder-card .el-icon {
  font-size: 40px;
}

.placeholder-card p {
  margin: 0;
  font-size: 13px;
}
</style>
