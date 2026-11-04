<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { portalApi } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  PortalUsageItem,
  PortalUsageListResponse,
} from '../api/types'
import { formatDateTime } from '../utils/datetime'

const rangeDays = ref(30)
const items = ref<PortalUsageItem[]>([])
const nextCursor = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const loadError = ref('')

function rangeParams(): URLSearchParams {
  const to = new Date()
  const from = new Date(to.getTime() - rangeDays.value * 24 * 3600 * 1000)
  return new URLSearchParams({
    from: from.toISOString(),
    to: to.toISOString(),
    limit: '50',
  })
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const response = await portalApi.get<PortalUsageListResponse>(
      `/me/usage?${rangeParams().toString()}`,
    )
    items.value = response.items
    nextCursor.value = response.next_cursor
  } catch (error) {
    loadError.value = apiErrorMessage(error, '用量明细加载失败')
    items.value = []
    nextCursor.value = ''
  } finally {
    loading.value = false
  }
}

async function loadMore(): Promise<void> {
  if (!nextCursor.value || loadingMore.value) return
  loadingMore.value = true
  try {
    const params = rangeParams()
    params.set('cursor', nextCursor.value)
    const response = await portalApi.get<PortalUsageListResponse>(
      `/me/usage?${params.toString()}`,
    )
    items.value = [...items.value, ...response.items]
    nextCursor.value = response.next_cursor
  } catch (error) {
    loadError.value = apiErrorMessage(error, '更多记录加载失败')
  } finally {
    loadingMore.value = false
  }
}

function statusType(status: number): 'success' | 'warning' | 'danger' {
  if (status >= 200 && status < 300) return 'success'
  if (status >= 400 && status < 500) return 'warning'
  return 'danger'
}

function dlpLabel(action: string): string {
  switch (action) {
    case 'block':
      return '已阻断'
    case 'mask':
      return '已脱敏'
    case 'audit':
      return '已记录'
    default:
      return '—'
  }
}

onMounted(load)
</script>

<template>
  <div class="usage-page">
    <section class="page-heading">
      <div>
        <h2>用量明细</h2>
        <p>仅展示调用元数据，不包含提示词或模型响应内容。</p>
      </div>
      <el-select
        v-model="rangeDays"
        class="range-select"
        @change="load"
      >
        <el-option label="最近 7 天" :value="7" />
        <el-option label="最近 30 天" :value="30" />
        <el-option label="最近 90 天" :value="90" />
      </el-select>
    </section>

    <el-alert
      v-if="loadError"
      :title="loadError"
      type="error"
      :closable="false"
      show-icon
      class="load-error"
    />

    <el-card class="table-card" shadow="never">
      <el-table v-loading="loading" :data="items" stripe>
        <el-table-column label="时间" width="165">
          <template #default="{ row }">{{ formatDateTime(row.event_time) }}</template>
        </el-table-column>
        <el-table-column prop="model" label="模型" min-width="160">
          <template #default="{ row }">{{ row.model || '—' }}</template>
        </el-table-column>
        <el-table-column prop="path" label="接口路径" min-width="170" />
        <el-table-column label="状态" width="95">
          <template #default="{ row }">
            <el-tag :type="statusType(row.status_code)" size="small">
              {{ row.status_code }}
            </el-tag>
            <div v-if="row.error_code" class="error-code">{{ row.error_code }}</div>
          </template>
        </el-table-column>
        <el-table-column label="Tokens" width="120">
          <template #default="{ row }">
            <span class="tokens">{{ row.total_tokens }}</span>
            <div class="token-split">
              {{ row.prompt_tokens }} / {{ row.completion_tokens }}
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="latency_ms" label="延迟" width="90">
          <template #default="{ row }">{{ row.latency_ms }} ms</template>
        </el-table-column>
        <el-table-column label="DLP" width="90">
          <template #default="{ row }">
            <el-tag
              v-if="row.dlp_action && row.dlp_action !== 'none'"
              size="small"
              type="warning"
            >
              {{ dlpLabel(row.dlp_action) }}
            </el-tag>
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="所选周期内暂无调用记录" :image-size="72" />
        </template>
      </el-table>

      <div v-if="nextCursor" class="load-more">
        <el-button :loading="loadingMore" @click="loadMore">加载更多</el-button>
      </div>
    </el-card>
  </div>
</template>

<style scoped>
.page-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
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

.range-select {
  width: 140px;
}

.load-error {
  margin-bottom: 16px;
}

.table-card {
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.table-card :deep(.el-card__body) {
  padding: 18px 20px;
}

.tokens {
  color: #26384d;
  font-weight: 600;
}

.token-split {
  color: #a0adbd;
  font-size: 10px;
}

.error-code {
  margin-top: 3px;
  color: var(--gw-danger);
  font-size: 10px;
}

.muted {
  color: #a8b4c2;
}

.load-more {
  display: flex;
  justify-content: center;
  margin-top: 16px;
}

@media (max-width: 640px) {
  .page-heading {
    flex-direction: column;
  }

  .range-select {
    width: 100%;
  }
}
</style>
