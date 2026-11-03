import {
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
} from 'vue'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  DashboardRange,
  DashboardSummary,
} from '../api/types'

const AUTO_REFRESH_MS = 60_000
const STALE_MS = AUTO_REFRESH_MS - 5_000

export interface DashboardFilters {
  range: DashboardRange
  departmentId: number | null
  providerId: string
}

export function useDashboard(initial: DashboardFilters) {
  const range = ref<DashboardRange>(initial.range)
  const departmentId = ref<number | null>(initial.departmentId)
  const providerId = ref(initial.providerId)

  const data = shallowRef<DashboardSummary | null>(null)
  const loading = ref(false)
  const refreshing = ref(false)
  const error = ref('')
  const lastUpdated = ref<Date | null>(null)

  let requestSeq = 0
  let timer: ReturnType<typeof setInterval> | null = null

  function buildQuery(): string {
    const params = new URLSearchParams({ range: range.value })
    if (departmentId.value !== null) {
      params.set('department_id', String(departmentId.value))
    }
    if (providerId.value) params.set('provider_id', providerId.value)
    return params.toString()
  }

  async function load(silent = false): Promise<void> {
    const seq = ++requestSeq
    if (!silent) {
      if (data.value) refreshing.value = true
      else loading.value = true
    }
    error.value = ''
    try {
      const result = await api.get<DashboardSummary>(
        `/dashboard/summary?${buildQuery()}`,
      )
      if (seq !== requestSeq) return
      data.value = result
      lastUpdated.value = new Date()
    } catch (err) {
      if (seq !== requestSeq) return
      error.value = apiErrorMessage(err, '概览数据加载失败')
    } finally {
      if (seq === requestSeq) {
        loading.value = false
        refreshing.value = false
      }
    }
  }

  function refresh(): Promise<void> {
    return load()
  }

  function refreshIfStale(): void {
    if (document.visibilityState !== 'visible') return
    const updated = lastUpdated.value
    if (!updated || Date.now() - updated.getTime() >= STALE_MS) {
      void load(true)
    }
  }

  function onVisibilityChange(): void {
    refreshIfStale()
  }

  function startAutoRefresh(): void {
    if (timer !== null) return
    timer = setInterval(refreshIfStale, AUTO_REFRESH_MS)
    document.addEventListener('visibilitychange', onVisibilityChange)
  }

  function stopAutoRefresh(): void {
    if (timer !== null) {
      clearInterval(timer)
      timer = null
    }
    document.removeEventListener('visibilitychange', onVisibilityChange)
  }

  watch([range, departmentId, providerId], () => {
    void load()
  })

  onMounted(() => {
    void load()
    startAutoRefresh()
  })

  onBeforeUnmount(stopAutoRefresh)

  return {
    range,
    departmentId,
    providerId,
    data,
    loading,
    refreshing,
    error,
    lastUpdated,
    load,
    refresh,
  }
}
