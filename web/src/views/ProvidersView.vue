<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  Provider,
  ProviderPayload,
  ProviderTestResult,
  Protocol,
} from '../api/types'
import { useAuthStore } from '../stores/auth'

interface ProviderForm {
  name: string
  protocol: Protocol
  base_url: string
  api_key: string
  is_default: boolean
  enabled: boolean
  timeout_seconds: number
}

interface ProviderListResponse {
  items: Provider[]
}

const auth = useAuthStore()
const canManage = computed(
  () => auth.isSuperAdmin || auth.user?.role === 'operator',
)

const providers = ref<Provider[]>([])
const loading = ref(false)
const loadError = ref('')
const testResults = ref<Record<string, ProviderTestResult>>({})
const testingID = ref('')

const dialogVisible = ref(false)
const editingProvider = ref<Provider | null>(null)
const saving = ref(false)
const formError = ref('')
const providerForm = ref<ProviderForm>({
  name: '',
  protocol: 'openai_compatible',
  base_url: '',
  api_key: '',
  is_default: false,
  enabled: true,
  timeout_seconds: 300,
})

const dialogTitle = computed(() =>
  editingProvider.value ? '编辑上游服务' : '新增上游服务',
)

const protocolLabels: Record<Protocol, string> = {
  openai_compatible: 'OpenAI 兼容',
  anthropic: 'Anthropic',
}

function protocolLabel(protocol: Protocol): string {
  return protocolLabels[protocol] ?? protocol
}

function apiKeyHint(provider: Provider): string {
  return provider.api_key_last4
    ? `•••• ${provider.api_key_last4}`
    : '已加密保存'
}

function testResultText(result: ProviderTestResult | undefined): string {
  if (!result) return '未测试'
  if (result.reachable) {
    if (result.message?.includes('API key')) return '可连通，Key 可能无效'
    if (result.message?.includes('messages endpoint')) return '可连通，但未找到消息端点'
    if (result.message?.includes('test model')) return '可连通，但测试模型不可用'
    if (result.message?.includes('rate limited')) return '可连通，但被限流'
    return result.status_code
      ? `连接正常（HTTP ${result.status_code}）`
      : '连接正常'
  }
  if (result.message === 'connection failed') return '连接失败'
  if (result.message === 'upstream returned an unexpected status') {
    return '上游返回异常状态'
  }
  return '连接失败'
}

function testResultType(
  result: ProviderTestResult | undefined,
): 'success' | 'warning' | 'danger' | 'info' {
  if (!result) return 'info'
  if (!result.reachable) return 'danger'
  return result.message ? 'warning' : 'success'
}

async function loadProviders(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const response = await api.get<ProviderListResponse>('/providers')
    providers.value = response.items
  } catch (error) {
    loadError.value = apiErrorMessage(error, '上游服务加载失败')
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editingProvider.value = null
  formError.value = ''
  providerForm.value.name = ''
  providerForm.value.protocol = 'openai_compatible'
  providerForm.value.base_url = ''
  providerForm.value.api_key = ''
  providerForm.value.is_default = false
  providerForm.value.enabled = true
  providerForm.value.timeout_seconds = 300
  dialogVisible.value = true
}

function openEdit(provider: Provider): void {
  editingProvider.value = provider
  formError.value = ''
  providerForm.value.name = provider.name
  providerForm.value.protocol = provider.protocol
  providerForm.value.base_url = provider.base_url
  providerForm.value.api_key = ''
  providerForm.value.is_default = provider.is_default
  providerForm.value.enabled = provider.enabled
  providerForm.value.timeout_seconds = provider.timeout_seconds
  dialogVisible.value = true
}

function validateForm(): string {
  if (!providerForm.value.name.trim()) return '请输入服务名称'
  if (providerForm.value.name.trim().length > 128) return '服务名称不能超过 128 个字符'
  if (!providerForm.value.base_url.trim()) return '请输入 Base URL'
  try {
    const parsed = new URL(providerForm.value.base_url.trim())
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return 'Base URL 必须是 http 或 https 地址'
    }
    if (parsed.username || parsed.password || parsed.search || parsed.hash) {
      return 'Base URL 不能包含账号、查询参数或锚点'
    }
  } catch {
    return 'Base URL 格式不正确'
  }
  if (!editingProvider.value && !providerForm.value.api_key.trim()) {
    return '请输入 API Key'
  }
  if (
    editingProvider.value &&
    providerForm.value.base_url.trim().replace(/\/+$/, '') !== editingProvider.value.base_url &&
    !providerForm.value.api_key.trim()
  ) {
    return '修改 Base URL 时必须重新填写 API Key'
  }
  if (
    !Number.isInteger(providerForm.value.timeout_seconds) ||
    providerForm.value.timeout_seconds < 1 ||
    providerForm.value.timeout_seconds > 600
  ) {
    return '超时时间必须在 1 到 600 秒之间'
  }
  if (providerForm.value.is_default && !providerForm.value.enabled) {
    return '默认服务必须处于启用状态'
  }
  return ''
}

async function save(): Promise<void> {
  formError.value = validateForm()
  if (formError.value) return

  const payload: ProviderPayload = {
    name: providerForm.value.name.trim(),
    protocol: providerForm.value.protocol,
    base_url: providerForm.value.base_url.trim().replace(/\/+$/, ''),
    api_key: providerForm.value.api_key.trim(),
    is_default: providerForm.value.is_default,
    enabled: providerForm.value.enabled,
    timeout_seconds: providerForm.value.timeout_seconds,
  }

  saving.value = true
  try {
    if (editingProvider.value) {
      await api.put<Provider>(
        `/providers/${editingProvider.value.id}`,
        payload,
      )
      ElMessage.success('上游服务已更新')
    } else {
      await api.post<Provider>('/providers', payload)
      ElMessage.success('上游服务已创建')
    }
    dialogVisible.value = false
    await loadProviders()
  } catch (error) {
    formError.value = apiErrorMessage(error)
  } finally {
    saving.value = false
  }
}

async function testProvider(provider: Provider): Promise<void> {
  testingID.value = provider.id
  try {
    const result = await api.post<ProviderTestResult>(
      `/providers/${provider.id}/test`,
    )
    testResults.value = { ...testResults.value, [provider.id]: result }
    if (result.reachable) {
      ElMessage.success(testResultText(result))
    } else {
      ElMessage.error(testResultText(result))
    }
  } catch (error) {
    const result: ProviderTestResult = {
      reachable: false,
      message: 'connection failed',
    }
    testResults.value = { ...testResults.value, [provider.id]: result }
    ElMessage.error(apiErrorMessage(error, '连通性测试失败'))
  } finally {
    testingID.value = ''
  }
}

async function toggleEnabled(provider: Provider): Promise<void> {
  const nextEnabled = !provider.enabled
  if (!nextEnabled) {
    try {
      await ElMessageBox.confirm(
        `禁用「${provider.name}」后，指向该服务的模型路由将无法转发请求。`,
        '确认禁用上游服务',
        {
          type: 'warning',
          confirmButtonText: '确认禁用',
          cancelButtonText: '取消',
        },
      )
    } catch {
      return
    }
  }

  try {
    await api.put<Provider>(`/providers/${provider.id}`, {
      name: provider.name,
      protocol: provider.protocol,
      base_url: provider.base_url,
      api_key: '',
      is_default: provider.is_default,
      enabled: nextEnabled,
      timeout_seconds: provider.timeout_seconds,
    } satisfies ProviderPayload)
    ElMessage.success(nextEnabled ? '上游服务已启用' : '上游服务已禁用')
    await loadProviders()
  } catch (error) {
    ElMessage.error(apiErrorMessage(error))
  }
}

async function removeProvider(provider: Provider): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `删除「${provider.name}」后无法恢复，且必须先解除模型路由对它的引用。`,
      '确认删除上游服务',
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
    await api.del(`/providers/${provider.id}`)
    ElMessage.success('上游服务已删除')
    await loadProviders()
  } catch (error) {
    ElMessage.error(apiErrorMessage(error))
  }
}

onMounted(loadProviders)</script>

<template>
  <div class="providers-page">
    <section class="page-heading">
      <div>
        <h2>上游服务</h2>
        <p>
          集中托管 OpenAI、Anthropic、DeepSeek 等上游凭据。API Key
          仅写入，保存后不可回显。
        </p>
      </div>
      <el-button
        v-if="canManage"
        data-test="create-provider"
        type="primary"
        :icon="Plus"
        @click="openCreate"
      >
        新增上游服务
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

      <el-table v-loading="loading" :data="providers" stripe>
        <el-table-column prop="name" label="名称" min-width="120">
          <template #default="{ row }">
            <div class="name-cell">
              <strong>{{ row.name }}</strong>
              <el-tag v-if="row.is_default" size="small" type="warning">
                默认
              </el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="协议" width="110">
          <template #default="{ row }">
            <el-tag size="small" effect="plain">
              {{ protocolLabel(row.protocol) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="base_url" label="Base URL" min-width="190" />
        <el-table-column label="API Key" width="120">
          <template #default="{ row }">{{ apiKeyHint(row) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="78">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
              {{ row.enabled ? '启用' : '禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="timeout_seconds"
          label="超时"
          width="78"
        >
          <template #default="{ row }">{{ row.timeout_seconds }} 秒</template>
        </el-table-column>
        <el-table-column label="连通性" width="130">
          <template #default="{ row }">
            <el-tag :type="testResultType(testResults[row.id])" size="small">
              {{ testResultText(testResults[row.id]) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column v-if="canManage" label="操作" width="210">
          <template #default="{ row }">
            <el-button
              link
              type="primary"
              :loading="testingID === row.id"
              data-test="test-provider"
              @click="testProvider(row)"
            >
              测试
            </el-button>
            <el-button link type="primary" @click="openEdit(row)">
              编辑
            </el-button>
            <el-button link @click="toggleEnabled(row)">
              {{ row.enabled ? '禁用' : '启用' }}
            </el-button>
            <el-button link type="danger" @click="removeProvider(row)">
              删除
            </el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无上游服务" :image-size="80" />
        </template>
      </el-table>
    </el-card>

    <el-dialog
      v-model="dialogVisible"
      :title="dialogTitle"
      width="620px"
      :close-on-click-modal="false"
    >
      <el-form label-position="top" @submit.prevent="save">
        <div class="form-grid">
          <el-form-item label="服务名称" required>
            <el-input
              v-model="providerForm.name"
              data-test="provider-name"
              maxlength="128"
              placeholder="例如 OpenAI 主账号"
            />
          </el-form-item>
          <el-form-item label="协议" required>
            <el-select v-model="providerForm.protocol" class="full-width">
              <el-option label="OpenAI 兼容" value="openai_compatible" />
              <el-option label="Anthropic" value="anthropic" />
            </el-select>
          </el-form-item>
        </div>

        <el-form-item label="Base URL" required>
          <el-input
            v-model="providerForm.base_url"
            data-test="provider-base-url"
            maxlength="512"
            placeholder="https://api.openai.com 或 https://api.deepseek.com/v1"
          />
        </el-form-item>

        <el-form-item :label="editingProvider ? 'API Key（留空表示不修改）' : 'API Key'" required>
          <el-input
            v-model="providerForm.api_key"
            data-test="provider-api-key"
            type="password"
            show-password
            autocomplete="new-password"
            maxlength="1024"
            :placeholder="editingProvider ? '不修改请留空' : '输入上游 API Key'"
          />
          <div class="field-hint">
            密钥使用 AES-256-GCM 加密保存，后台仅展示末四位。
            <template v-if="editingProvider">
              修改 Base URL 时必须重新输入 Key。
            </template>
          </div>
        </el-form-item>

        <div class="form-grid">
          <el-form-item label="超时时间（秒）">
            <el-input-number
              v-model="providerForm.timeout_seconds"
              :min="1"
              :max="600"
              :step="10"
              controls-position="right"
              class="full-width"
            />
          </el-form-item>
          <el-form-item label="默认服务">
            <el-switch
              v-model="providerForm.is_default"
              active-text="是"
              inactive-text="否"
            />
          </el-form-item>
        </div>

        <el-form-item label="启用">
          <el-switch
            v-model="providerForm.enabled"
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
          data-test="save-provider"
          type="primary"
          :loading="saving"
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
  max-width: 720px;
  margin: 0;
  color: var(--gw-text-muted);
  font-size: 13px;
  line-height: 1.65;
}

.table-card {
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.table-card :deep(.el-card__body) {
  padding: 22px 22px 18px;
}

.load-error {
  margin-bottom: 16px;
}

.name-cell {
  display: flex;
  align-items: center;
  gap: 8px;
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
  color: var(--gw-text-muted);
  font-size: 12px;
  line-height: 1.5;
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
