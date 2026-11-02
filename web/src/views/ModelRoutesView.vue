<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  ModelRoute,
  ModelRoutePayload,
  Protocol,
  Provider,
  RouteMatchType,
} from '../api/types'
import { useAuthStore } from '../stores/auth'

interface ListResponse<T> {
  items: T[]
}

interface RouteForm {
  provider_id: string
  protocol: Protocol
  pattern: string
  match_type: RouteMatchType
  upstream_model: string
  priority: number
  enabled: boolean
}

const auth = useAuthStore()
const canManage = computed(
  () => auth.isSuperAdmin || auth.user?.role === 'operator',
)

const routes = ref<ModelRoute[]>([])
const providers = ref<Provider[]>([])
const loading = ref(false)
const loadError = ref('')

const dialogVisible = ref(false)
const saving = ref(false)
const formError = ref('')
const form = reactive<RouteForm>({
  provider_id: '',
  protocol: 'openai_compatible',
  pattern: '',
  match_type: 'prefix',
  upstream_model: '',
  priority: 100,
  enabled: true,
})

const providerMap = computed(
  () => new Map(providers.value.map((provider) => [provider.id, provider])),
)

const protocolLabels: Record<Protocol, string> = {
  openai_compatible: 'OpenAI 兼容',
  anthropic: 'Anthropic',
}

const matchTypeLabels: Record<RouteMatchType, string> = {
  exact: '精确匹配',
  prefix: '前缀匹配',
  glob: '通配符',
}

function protocolLabel(protocol: Protocol): string {
  return protocolLabels[protocol] ?? protocol
}

function matchTypeLabel(matchType: RouteMatchType): string {
  return matchTypeLabels[matchType] ?? matchType
}

function providerName(providerID: string): string {
  return providerMap.value.get(providerID)?.name ?? providerID.slice(0, 8)
}

async function loadData(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const [routeResponse, providerResponse] = await Promise.all([
      api.get<ListResponse<ModelRoute>>('/model-routes'),
      api.get<ListResponse<Provider>>('/providers'),
    ])
    routes.value = routeResponse.items
    providers.value = providerResponse.items
  } catch (error) {
    loadError.value = apiErrorMessage(error, '模型路由加载失败')
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  if (providers.value.length === 0) {
    ElMessage.warning('请先配置至少一个上游服务')
    return
  }
  formError.value = ''
  form.provider_id = providers.value[0]?.id ?? ''
  form.protocol = providers.value[0]?.protocol ?? 'openai_compatible'
  form.pattern = ''
  form.match_type = 'prefix'
  form.upstream_model = ''
  form.priority = 100
  form.enabled = true
  dialogVisible.value = true
}

function onProviderChange(providerID: string): void {
  const provider = providerMap.value.get(providerID)
  if (provider) form.protocol = provider.protocol
}

function validateForm(): string {
  const provider = providerMap.value.get(form.provider_id)
  if (!provider) return '请选择上游服务'
  if (form.protocol !== provider.protocol) {
    return `协议不一致：所选上游服务使用「${protocolLabel(provider.protocol)}」`
  }
  const pattern = form.pattern.trim()
  if (!pattern) return '请输入匹配 pattern'
  if (pattern.length > 255) return 'pattern 不能超过 255 个字符'
  if (!Number.isInteger(form.priority) || form.priority < 0) {
    return '优先级必须是非负整数'
  }
  return ''
}

async function save(): Promise<void> {
  formError.value = validateForm()
  if (formError.value) return

  const payload: ModelRoutePayload = {
    provider_id: form.provider_id,
    protocol: form.protocol,
    pattern: form.pattern.trim(),
    match_type: form.match_type,
    upstream_model: form.upstream_model.trim(),
    priority: form.priority,
    enabled: form.enabled,
  }

  saving.value = true
  try {
    await api.post<ModelRoute>('/model-routes', payload)
    ElMessage.success('模型路由已创建')
    dialogVisible.value = false
    await loadData()
  } catch (error) {
    formError.value = apiErrorMessage(error)
  } finally {
    saving.value = false
  }
}

async function removeRoute(route: ModelRoute): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `删除「${route.pattern}」路由后，匹配该模型的请求将无法转发，且无法恢复。`,
      '确认删除模型路由',
      {
        type: 'warning',
        confirmButtonText: '确认删除',
        cancelButtonText: '取消',
      },
    )
  } catch {
    return
  }

  try {
    await api.del(`/model-routes/${route.id}`)
    ElMessage.success('模型路由已删除')
    await loadData()
  } catch (error) {
    ElMessage.error(apiErrorMessage(error))
  }
}

onMounted(loadData)
</script>

<template>
  <div class="model-routes-page">
    <section class="page-heading">
      <div>
        <h2>模型路由</h2>
        <p>
          将客户端请求的模型名称匹配到指定上游服务。路由协议必须与上游服务协议一致。
        </p>
      </div>
      <el-button
        v-if="canManage"
        data-test="create-route"
        type="primary"
        :icon="Plus"
        @click="openCreate"
      >
        新增路由
      </el-button>
    </section>

    <el-card class="table-card" shadow="never">
      <el-alert
        v-if="loadError"
        :title="loadError"
        type="error"
        :closable="false"
        show-icon
        class="load-error"
      />

      <el-table v-loading="loading" :data="routes" stripe>
        <el-table-column prop="pattern" label="匹配 Pattern" min-width="200">
          <template #default="{ row }">
            <code class="pattern">{{ row.pattern }}</code>
          </template>
        </el-table-column>
        <el-table-column label="匹配方式" width="120">
          <template #default="{ row }">
            {{ matchTypeLabel(row.match_type) }}
          </template>
        </el-table-column>
        <el-table-column label="协议" width="130">
          <template #default="{ row }">
            <el-tag size="small" effect="plain">
              {{ protocolLabel(row.protocol) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="上游服务" min-width="150">
          <template #default="{ row }">{{ providerName(row.provider_id) }}</template>
        </el-table-column>
        <el-table-column label="上游模型" min-width="160">
          <template #default="{ row }">{{ row.upstream_model || '同名透传' }}</template>
        </el-table-column>
        <el-table-column prop="priority" label="优先级" width="90" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
              {{ row.enabled ? '启用' : '禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column v-if="canManage" label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-button
              link
              type="danger"
              data-test="delete-route"
              @click="removeRoute(row)"
            >
              删除
            </el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无模型路由" :image-size="80" />
        </template>
      </el-table>
    </el-card>

    <el-dialog
      v-model="dialogVisible"
      title="新增模型路由"
      width="620px"
      :close-on-click-modal="false"
    >
      <el-form label-position="top" @submit.prevent="save">
        <el-alert
          v-if="providers.length === 0"
          title="请先配置上游服务"
          type="warning"
          :closable="false"
          show-icon
          class="form-alert"
        />

        <div class="form-grid">
          <el-form-item label="上游服务" required>
            <el-select
              v-model="form.provider_id"
              data-test="route-provider"
              class="full-width"
              placeholder="选择上游服务"
              @change="onProviderChange"
            >
              <el-option
                v-for="provider in providers"
                :key="provider.id"
                :label="`${provider.name}（${protocolLabel(provider.protocol)}）`"
                :value="provider.id"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="协议" required>
            <el-select
              v-model="form.protocol"
              data-test="route-protocol"
              class="full-width"
            >
              <el-option label="OpenAI 兼容" value="openai_compatible" />
              <el-option label="Anthropic" value="anthropic" />
            </el-select>
          </el-form-item>
        </div>

        <div class="form-grid">
          <el-form-item label="匹配方式" required>
            <el-select v-model="form.match_type" class="full-width">
              <el-option label="精确匹配" value="exact" />
              <el-option label="前缀匹配" value="prefix" />
              <el-option label="通配符" value="glob" />
            </el-select>
          </el-form-item>
          <el-form-item label="优先级">
            <el-input-number
              v-model="form.priority"
              :min="0"
              :controls="false"
              class="full-width"
            />
          </el-form-item>
        </div>

        <el-form-item label="匹配 Pattern" required>
          <el-input
            v-model="form.pattern"
            data-test="route-pattern"
            maxlength="255"
            placeholder="例如 gpt-4o、claude-*"
          />
          <div class="field-hint">
            精确匹配要求名称完全一致；前缀匹配以该值开头；通配符遵循 glob
            规则。
          </div>
        </el-form-item>

        <el-form-item label="上游模型">
          <el-input
            v-model="form.upstream_model"
            data-test="route-upstream-model"
            maxlength="255"
            placeholder="留空则使用客户端请求的模型名称"
          />
        </el-form-item>

        <el-form-item label="启用">
          <el-switch
            v-model="form.enabled"
            active-text="启用"
            inactive-text="禁用"
          />
        </el-form-item>

        <el-alert
          v-if="formError"
          :title="formError"
          type="error"
          :closable="false"
          show-icon
        />
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button
          data-test="save-route"
          type="primary"
          :loading="saving"
          :disabled="providers.length === 0"
          @click="save"
        >
          保存
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 22px;
}

.page-heading h2 {
  margin: 0 0 8px;
  color: #1f2d3d;
  font-size: 22px;
}

.page-heading p {
  max-width: 720px;
  margin: 0;
  color: #8393a7;
  font-size: 14px;
  line-height: 1.6;
}

.table-card {
  border: 1px solid #e8edf4;
  border-radius: 12px;
}

.load-error {
  margin-bottom: 16px;
}

.pattern {
  padding: 2px 8px;
  border-radius: 5px;
  background: #f1f5fa;
  color: #334155;
  font-size: 12px;
}

.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

.full-width {
  width: 100%;
}

.field-hint {
  margin-top: 6px;
  color: #98a6b7;
  font-size: 12px;
  line-height: 1.5;
}

.form-alert {
  margin-bottom: 16px;
}

@media (max-width: 640px) {
  .page-heading {
    flex-direction: column;
  }

  .form-grid {
    grid-template-columns: 1fr;
    gap: 0;
  }
}
</style>
