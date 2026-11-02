<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { CopyDocument } from '@element-plus/icons-vue'

import type {
  Employee,
  QuotaPeriod,
  QuotaType,
  Token,
  TokenPayload,
} from '../api/types'
import { toShanghaiInput } from '../utils/datetime'

const props = withDefaults(
  defineProps<{
    modelValue: boolean
    employees?: Employee[]
    initial?: Token | null
    token?: string | null
    saving?: boolean
    errorMessage?: string
    title?: string
  }>(),
  {
    employees: () => [],
    initial: null,
    token: null,
    saving: false,
    errorMessage: '',
    title: '新增 Token',
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  submit: [payload: TokenPayload]
  closed: []
}>()

interface TokenForm {
  employee_id: string
  quota_type: QuotaType
  quota_period: QuotaPeriod
  quota_total: number | undefined
  rate_limit_rpm: number | undefined
  expires_at: string
  ip_allowlist: string
}

const form = reactive<TokenForm>({
  employee_id: '',
  quota_type: 'tokens',
  quota_period: 'monthly',
  quota_total: undefined,
  rate_limit_rpm: 60,
  expires_at: '',
  ip_allowlist: '',
})

const validationError = ref('')
const copyState = ref<'idle' | 'copied' | 'failed'>('idle')
let copyTimer: ReturnType<typeof setTimeout> | undefined

const displayError = computed(
  () => validationError.value || props.errorMessage,
)

const editing = computed(() => props.initial !== null)

const copyLabel = computed(() => {
  if (copyState.value === 'copied') return '已复制'
  if (copyState.value === 'failed') return '复制失败'
  return '复制 Token'
})

function resetForm(): void {
  const initial = props.initial
  form.employee_id = initial?.employee_id ?? props.employees[0]?.id ?? ''
  form.quota_type = initial?.quota_type ?? 'tokens'
  form.quota_period = initial?.quota_period ?? 'monthly'
  form.quota_total = initial?.quota_total ?? undefined
  form.rate_limit_rpm = initial?.rate_limit_rpm ?? 60
  form.expires_at = toShanghaiInput(initial?.expires_at)
  form.ip_allowlist = (initial?.ip_allowlist ?? []).join('\n')
  validationError.value = ''
  copyState.value = 'idle'
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) resetForm()
  },
  { immediate: true },
)

watch(
  () => props.employees,
  (employees) => {
    if (!form.employee_id && !editing.value && employees.length > 0) {
      form.employee_id = employees[0].id
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  if (copyTimer) clearTimeout(copyTimer)
})

function close(): void {
  emit('update:modelValue', false)
}

function submitForm(): void {
  validationError.value = ''
  form.employee_id = form.employee_id.trim()

  if (!form.employee_id) {
    validationError.value = '请选择员工'
    return
  }
  if (
    typeof form.rate_limit_rpm !== 'number' ||
    !Number.isInteger(form.rate_limit_rpm) ||
    form.rate_limit_rpm < 1 ||
    form.rate_limit_rpm > 100000
  ) {
    validationError.value = '每分钟请求数必须在 1 到 100000 之间'
    return
  }
  if (
    form.quota_total !== undefined &&
    (!Number.isInteger(form.quota_total) || form.quota_total < 0)
  ) {
    validationError.value = '额度总量必须是非负整数'
    return
  }

  const ipAllowlist = form.ip_allowlist
    .split(/\r?\n/)
    .map((value) => value.trim())
    .filter(Boolean)

  emit('submit', {
    employee_id: form.employee_id,
    quota_type: form.quota_type,
    quota_period: form.quota_period,
    quota_total: form.quota_total ?? null,
    rate_limit_rpm: form.rate_limit_rpm,
    expires_at: form.expires_at || null,
    ip_allowlist: ipAllowlist,
  })
}

async function copyToken(): Promise<void> {
  if (!props.token) return
  try {
    if (!navigator.clipboard?.writeText) {
      throw new Error('clipboard unavailable')
    }
    await navigator.clipboard.writeText(props.token)
    copyState.value = 'copied'
  } catch {
    copyState.value = 'failed'
  }
  if (copyTimer) clearTimeout(copyTimer)
  copyTimer = setTimeout(() => {
    copyState.value = 'idle'
  }, 2000)
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    :title="title"
    width="580px"
    :close-on-click-modal="false"
    @update:model-value="emit('update:modelValue', $event)"
    @closed="emit('closed')"
  >
    <template v-if="token">
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        title="完整 Token 仅显示一次"
        description="请立即复制并妥善保存。关闭此窗口后将无法再次查看完整 Token。"
      />
      <div class="token-box">
        <code data-test="token-value">{{ token }}</code>
        <el-button
          data-test="copy-token"
          :icon="CopyDocument"
          plain
          @click="copyToken"
        >
          {{ copyLabel }}
        </el-button>
      </div>
    </template>

    <el-form v-else label-position="top" @submit.prevent="submitForm">
      <el-form-item label="员工" required>
        <el-alert
          v-if="employees.length === 0"
          data-test="no-employees"
          title="暂无可用员工，请先在员工管理中创建员工"
          type="info"
          :closable="false"
          show-icon
        />
        <el-select
          v-else
          v-model="form.employee_id"
          data-test="token-employee"
          filterable
          placeholder="选择员工"
          :disabled="editing"
          class="full-width"
        >
          <el-option
            v-for="employee in employees"
            :key="employee.id"
            :label="`${employee.name}（${employee.employee_no}）`"
            :value="employee.id"
          />
        </el-select>
      </el-form-item>

      <div class="form-grid">
        <el-form-item label="额度类型">
          <el-select v-model="form.quota_type" class="full-width">
            <el-option label="Token 数" value="tokens" />
            <el-option label="请求数" value="requests" />
          </el-select>
        </el-form-item>
        <el-form-item label="额度周期">
          <el-select v-model="form.quota_period" class="full-width">
            <el-option label="每日" value="daily" />
            <el-option label="每月" value="monthly" />
            <el-option label="总量" value="total" />
          </el-select>
        </el-form-item>
      </div>

      <div class="form-grid">
        <el-form-item label="额度总量">
          <el-input-number
            v-model="form.quota_total"
            :min="0"
            :step="1000"
            :controls="false"
            placeholder="留空表示不限"
            class="full-width"
          />
        </el-form-item>
        <el-form-item label="每分钟请求数（RPM）" required>
          <el-input-number
            v-model="form.rate_limit_rpm"
            :min="1"
            :max="100000"
            :controls="false"
            class="full-width"
          />
        </el-form-item>
      </div>

      <el-form-item label="过期时间（北京时间）">
        <el-date-picker
          v-model="form.expires_at"
          type="datetime"
          value-format="YYYY-MM-DDTHH:mm:ss"
          placeholder="留空表示永不过期"
          class="full-width"
        />
      </el-form-item>

      <el-form-item label="IP 白名单">
        <el-input
          v-model="form.ip_allowlist"
          type="textarea"
          :rows="3"
          placeholder="每行一个 IP 或 CIDR，例如 10.0.0.0/8；留空表示不限制"
        />
      </el-form-item>

      <el-alert
        v-if="displayError"
        data-test="token-form-error"
        :title="displayError"
        type="error"
        :closable="false"
        show-icon
      />

      <p class="dialog-hint">
        每个员工可拥有多个 Token；轮换会立即撤销旧 Token 并生成新 Token。
      </p>
    </el-form>

    <template #footer>
      <template v-if="token">
        <el-button data-test="confirm-saved" type="primary" @click="close">
          我已保存
        </el-button>
      </template>
      <template v-else>
        <el-button @click="close">取消</el-button>
        <el-button
          data-test="save-token"
          type="primary"
          :loading="saving"
          @click="submitForm"
        >
          {{ editing ? '保存修改' : '创建 Token' }}
        </el-button>
      </template>
    </template>
  </el-dialog>
</template>

<style scoped>
.full-width {
  width: 100%;
}

.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

.token-box {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 18px;
  padding: 16px;
  border: 1px solid #d9e2ec;
  border-radius: 10px;
  background: #f7f9fc;
}

.token-box code {
  flex: 1;
  min-width: 0;
  color: #1f2d3d;
  font-family: ui-monospace, Consolas, monospace;
  font-size: 13px;
  overflow-wrap: anywhere;
  user-select: all;
}

.dialog-hint {
  margin: 14px 0 0;
  color: #8a99ab;
  font-size: 12px;
  line-height: 1.6;
}

@media (max-width: 640px) {
  .form-grid {
    grid-template-columns: 1fr;
    gap: 0;
  }

  .token-box {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
