<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Upload } from '@element-plus/icons-vue'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  DLPAction,
  DLPHit,
  DLPMatchType,
  DLPRule,
  DLPRulePayload,
  DLPTestResult,
} from '../api/types'
import { useAuthStore } from '../stores/auth'

type RuleFilter = 'all' | 'builtin' | 'custom'

interface RuleListResponse {
  items: DLPRule[]
}

interface RuleForm {
  name: string
  description: string
  category: string
  match_type: DLPMatchType
  pattern: string
  action: DLPAction
  replacement: string
  priority: number
  enabled: boolean
  alert_enabled: boolean
}

interface KeywordForm {
  name: string
  category: string
  action: DLPAction
  replacement: string
  priority: number
  enabled: boolean
  alert_enabled: boolean
  keywords: string
}

const auth = useAuthStore()
const canManage = computed(
  () => auth.isSuperAdmin || auth.user?.role === 'operator',
)

const rules = ref<DLPRule[]>([])
const loading = ref(false)
const loadError = ref('')
const filter = ref<RuleFilter>('all')

const dialogVisible = ref(false)
const editingRule = ref<DLPRule | null>(null)
const saving = ref(false)
const formError = ref('')
const form = reactive<RuleForm>({
  name: '',
  description: '',
  category: 'custom',
  match_type: 'keyword',
  pattern: '',
  action: 'mask',
  replacement: '***',
  priority: 100,
  enabled: true,
  alert_enabled: false,
})

const keywordDialogVisible = ref(false)
const keywordSaving = ref(false)
const keywordError = ref('')
const keywordForm = reactive<KeywordForm>({
  name: '',
  category: 'custom',
  action: 'mask',
  replacement: '***',
  priority: 100,
  enabled: true,
  alert_enabled: false,
  keywords: '',
})

const sampleText = ref('')
const testing = ref(false)
const testError = ref('')
const testResult = ref<DLPTestResult | null>(null)

const categoryLabels: Record<string, string> = {
  pii: '个人信息',
  network: '网络信息',
  credentials: '凭据密钥',
  contact: '联系方式',
  project: '项目信息',
  custom: '自定义',
}

const categoryOptions = [
  { label: '个人信息', value: 'pii' },
  { label: '网络信息', value: 'network' },
  { label: '凭据密钥', value: 'credentials' },
  { label: '联系方式', value: 'contact' },
  { label: '项目信息', value: 'project' },
  { label: '自定义', value: 'custom' },
]

const actionLabels: Record<DLPAction | 'none', string> = {
  block: '阻断',
  mask: '脱敏',
  audit: '仅审计',
  none: '未命中',
}

const actionTagTypes: Record<
  DLPAction | 'none',
  'danger' | 'warning' | 'info' | 'success'
> = {
  block: 'danger',
  mask: 'warning',
  audit: 'info',
  none: 'success',
}

const matchTypeLabels: Record<DLPMatchType, string> = {
  regex: '正则',
  keyword: '关键词',
}

const dialogTitle = computed(() =>
  editingRule.value ? '编辑 DLP 规则' : '新增 DLP 规则',
)

const isBuiltinEditing = computed(() => editingRule.value?.builtin === true)

const filteredRules = computed(() => {
  if (filter.value === 'builtin') return rules.value.filter((rule) => rule.builtin)
  if (filter.value === 'custom') return rules.value.filter((rule) => !rule.builtin)
  return rules.value
})

const filterCounts = computed(() => ({
  all: rules.value.length,
  builtin: rules.value.filter((rule) => rule.builtin).length,
  custom: rules.value.filter((rule) => !rule.builtin).length,
}))

function categoryLabel(category: string): string {
  return categoryLabels[category] ?? category
}

function actionLabel(action: DLPAction): string {
  return actionLabels[action] ?? action
}

function actionTagType(
  action: DLPAction,
): 'danger' | 'warning' | 'info' | 'success' {
  return actionTagTypes[action] ?? 'info'
}

function matchTypeLabel(matchType: DLPMatchType): string {
  return matchTypeLabels[matchType] ?? matchType
}

async function loadRules(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const response = await api.get<RuleListResponse>('/dlp-rules')
    rules.value = response.items
  } catch (error) {
    loadError.value = apiErrorMessage(error, 'DLP 规则加载失败')
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editingRule.value = null
  formError.value = ''
  form.name = ''
  form.description = ''
  form.category = 'custom'
  form.match_type = 'keyword'
  form.pattern = ''
  form.action = 'mask'
  form.replacement = '***'
  form.priority = 100
  form.enabled = true
  form.alert_enabled = false
  dialogVisible.value = true
}

function openEdit(rule: DLPRule): void {
  editingRule.value = rule
  formError.value = ''
  form.name = rule.name
  form.description = rule.description
  form.category = rule.category
  form.match_type = rule.match_type
  form.pattern = rule.pattern
  form.action = rule.action
  form.replacement = rule.replacement || '***'
  form.priority = rule.priority
  form.enabled = rule.enabled
  form.alert_enabled = rule.alert_enabled
  dialogVisible.value = true
}

function validateRule(rule: RuleForm): string {
  if (!rule.name.trim()) return '请输入规则名称'
  if (rule.name.trim().length > 128) return '规则名称不能超过 128 个字符'
  if (!rule.pattern.trim()) return '请输入匹配内容'
  if (rule.pattern.length > 4096) return '匹配内容不能超过 4096 个字符'
  if (!Number.isInteger(rule.priority) || rule.priority < 0) {
    return '优先级必须是非负整数'
  }
  if (!rule.replacement.trim()) return '请输入脱敏替换文本'
  if (rule.replacement.length > 128) return '替换文本不能超过 128 个字符'
  if (rule.match_type === 'regex') {
    try {
      new RegExp(rule.pattern)
    } catch {
      return '正则表达式格式不正确'
    }
  }
  return ''
}

async function saveRule(): Promise<void> {
  formError.value = validateRule(form)
  if (formError.value) return

  const payload: DLPRulePayload = {
    name: form.name.trim(),
    description: form.description.trim(),
    category: form.category,
    match_type: form.match_type,
    pattern: form.pattern,
    action: form.action,
    replacement: form.replacement.trim(),
    priority: form.priority,
    enabled: form.enabled,
    alert_enabled: form.alert_enabled,
  }

  saving.value = true
  try {
    if (editingRule.value) {
      await api.put<DLPRule>(`/dlp-rules/${editingRule.value.id}`, payload)
      ElMessage.success('DLP 规则已更新')
    } else {
      await api.post<DLPRule>('/dlp-rules', payload)
      ElMessage.success('DLP 规则已创建')
    }
    dialogVisible.value = false
    await loadRules()
  } catch (error) {
    formError.value = apiErrorMessage(error)
  } finally {
    saving.value = false
  }
}

function openKeywordImport(): void {
  keywordError.value = ''
  keywordForm.name = ''
  keywordForm.category = 'custom'
  keywordForm.action = 'mask'
  keywordForm.replacement = '***'
  keywordForm.priority = 100
  keywordForm.enabled = true
  keywordForm.alert_enabled = false
  keywordForm.keywords = ''
  keywordDialogVisible.value = true
}

function parseKeywords(raw: string): string[] {
  const seen = new Set<string>()
  const keywords: string[] = []
  for (const line of raw.split(/\r?\n/)) {
    const keyword = line.trim()
    if (!keyword) continue
    const key = keyword.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    keywords.push(keyword)
  }
  return keywords
}

function escapeRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

async function saveKeywords(): Promise<void> {
  keywordError.value = ''
  const name = keywordForm.name.trim()
  if (!name) {
    keywordError.value = '请输入规则名称'
    return
  }
  const keywords = parseKeywords(keywordForm.keywords)
  if (keywords.length === 0) {
    keywordError.value = '请至少输入一个关键词'
    return
  }
  if (keywords.length > 100) {
    keywordError.value = '单次最多导入 100 个关键词'
    return
  }
  if (keywords.some((keyword) => keyword.length > 128)) {
    keywordError.value = '单个关键词不能超过 128 个字符'
    return
  }
  if (!keywordForm.replacement.trim()) {
    keywordError.value = '请输入脱敏替换文本'
    return
  }

  const pattern = `(?i)(?:${keywords.map(escapeRegex).join('|')})`
  if (pattern.length > 4096) {
    keywordError.value = '关键词总长度过长，请分批导入'
    return
  }

  keywordSaving.value = true
  try {
    await api.post<DLPRule>('/dlp-rules', {
      name,
      description: `批量导入关键词规则（${keywords.length} 个关键词）`,
      category: keywordForm.category,
      match_type: 'regex',
      pattern,
      action: keywordForm.action,
      replacement: keywordForm.replacement.trim(),
      priority: keywordForm.priority,
      enabled: keywordForm.enabled,
      alert_enabled: keywordForm.alert_enabled,
    } satisfies DLPRulePayload)
    ElMessage.success(`已导入 ${keywords.length} 个关键词`)
    keywordDialogVisible.value = false
    await loadRules()
  } catch (error) {
    keywordError.value = apiErrorMessage(error)
  } finally {
    keywordSaving.value = false
  }
}

async function toggleEnabled(rule: DLPRule): Promise<void> {
  const nextEnabled = !rule.enabled
  if (!nextEnabled) {
    try {
      await ElMessageBox.confirm(
        `禁用「${rule.name}」后，该规则将不再参与请求检查。`,
        '确认禁用 DLP 规则',
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
    await api.put<DLPRule>(`/dlp-rules/${rule.id}`, {
      name: rule.name,
      description: rule.description,
      category: rule.category,
      match_type: rule.match_type,
      pattern: rule.pattern,
      action: rule.action,
      replacement: rule.replacement,
      priority: rule.priority,
      enabled: nextEnabled,
      alert_enabled: rule.alert_enabled,
    } satisfies DLPRulePayload)
    ElMessage.success(nextEnabled ? '规则已启用' : '规则已禁用')
    await loadRules()
  } catch (error) {
    ElMessage.error(apiErrorMessage(error))
  }
}

async function removeRule(rule: DLPRule): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `删除「${rule.name}」后无法恢复，历史审计记录不受影响。`,
      '确认删除 DLP 规则',
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
    await api.del(`/dlp-rules/${rule.id}`)
    ElMessage.success('DLP 规则已删除')
    await loadRules()
  } catch (error) {
    ElMessage.error(apiErrorMessage(error))
  }
}

async function runTest(): Promise<void> {
  testError.value = ''
  testResult.value = null
  if (!sampleText.value.trim()) {
    testError.value = '请输入测试文本'
    return
  }

  testing.value = true
  try {
    testResult.value = await api.post<DLPTestResult>('/dlp-rules/test', {
      text: sampleText.value,
    })
  } catch (error) {
    testError.value = apiErrorMessage(error, 'DLP 测试失败')
  } finally {
    testing.value = false
  }
}

function hitSummary(hit: DLPHit): string {
  return hit.alert_enabled ? '已开启告警' : '不告警'
}

onMounted(loadRules)
</script>

<template>
  <div class="dlp-page">
    <section class="page-heading">
      <div>
        <h2>DLP 规则</h2>
        <p>
          识别身份证、手机号、银行卡、内网 IP、云密钥与项目敏感信息，按规则执行阻断、脱敏或仅审计。
        </p>
      </div>
      <div v-if="canManage" class="heading-actions">
        <el-button
          data-test="import-keywords"
          :icon="Upload"
          @click="openKeywordImport"
        >
          批量导入关键词
        </el-button>
        <el-button
          data-test="create-dlp-rule"
          type="primary"
          :icon="Plus"
          @click="openCreate"
        >
          新增规则
        </el-button>
      </div>
    </section>

    <el-card class="table-card" shadow="never">
      <div class="toolbar">
        <el-radio-group v-model="filter" size="small">
          <el-radio-button value="all">
            全部（{{ filterCounts.all }}）
          </el-radio-button>
          <el-radio-button value="builtin">
            内置（{{ filterCounts.builtin }}）
          </el-radio-button>
          <el-radio-button value="custom">
            自定义（{{ filterCounts.custom }}）
          </el-radio-button>
        </el-radio-group>
      </div>

      <el-alert
        v-if="loadError"
        :title="loadError"
        type="error"
        :closable="false"
        show-icon
        class="load-error"
      />

      <el-table v-loading="loading" :data="filteredRules" stripe>
        <el-table-column label="规则" min-width="190">
          <template #default="{ row }">
            <div class="rule-name">
              <strong>{{ row.name }}</strong>
              <el-tag v-if="row.builtin" size="small" type="info">内置</el-tag>
            </div>
            <span v-if="row.description" class="rule-description">
              {{ row.description }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="分类" width="110">
          <template #default="{ row }">{{ categoryLabel(row.category) }}</template>
        </el-table-column>
        <el-table-column label="匹配" width="100">
          <template #default="{ row }">
            {{ matchTypeLabel(row.match_type) }}
          </template>
        </el-table-column>
        <el-table-column label="动作" width="90">
          <template #default="{ row }">
            <el-tag :type="actionTagType(row.action)" size="small">
              {{ actionLabel(row.action) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="priority" label="优先级" width="90" />
        <el-table-column label="告警" width="90">
          <template #default="{ row }">
            <el-tag
              :type="row.alert_enabled ? 'warning' : 'info'"
              size="small"
              effect="plain"
            >
              {{ row.alert_enabled ? '开启' : '关闭' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
              {{ row.enabled ? '启用' : '禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column v-if="canManage" label="操作" width="190" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button link @click="toggleEnabled(row)">
              {{ row.enabled ? '禁用' : '启用' }}
            </el-button>
            <el-button
              v-if="!row.builtin"
              link
              type="danger"
              @click="removeRule(row)"
            >
              删除
            </el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无 DLP 规则" :image-size="80" />
        </template>
      </el-table>
    </el-card>

    <el-card class="test-card" shadow="never">
      <div class="test-heading">
        <div>
          <h3>规则测试</h3>
          <p>使用当前已启用的规则检查文本，不会写入审计日志。</p>
        </div>
      </div>

      <div class="test-layout">
        <div class="test-input">
          <el-input
            v-model="sampleText"
            data-test="dlp-sample"
            type="textarea"
            :rows="7"
            maxlength="20000"
            show-word-limit
            placeholder="粘贴需要检测的文本，例如包含手机号、身份证号或密钥的内容"
          />
          <div class="test-actions">
            <el-button
              data-test="dlp-test"
              type="primary"
              :loading="testing"
              @click="runTest"
            >
              测试
            </el-button>
          </div>
          <el-alert
            v-if="testError"
            :title="testError"
            type="error"
            :closable="false"
            show-icon
          />
        </div>

        <div class="test-output">
          <template v-if="testResult">
            <div class="result-heading">
              <span>处置结果</span>
              <el-tag :type="actionTagTypes[testResult.action]" size="small">
                {{ actionLabels[testResult.action] }}
              </el-tag>
            </div>

            <div v-if="testResult.hits.length === 0" class="no-hits">
              未命中敏感内容
            </div>
            <div v-else class="hit-list">
              <div
                v-for="(hit, index) in testResult.hits"
                :key="`${hit.rule_id}-${index}`"
                class="hit-item"
              >
                <strong>{{ hit.rule_name }}</strong>
                <el-tag :type="actionTagType(hit.action)" size="small">
                  {{ actionLabel(hit.action) }}
                </el-tag>
                <span class="hit-meta">{{ hitSummary(hit) }}</span>
              </div>
            </div>

            <div class="masked-heading">脱敏后文本</div>
            <pre data-test="dlp-masked" class="masked-text">{{ testResult.masked_text }}</pre>
          </template>
          <div v-else class="result-placeholder">
            测试结果将在这里显示，包含命中规则、处置动作与脱敏后文本。
          </div>
        </div>
      </div>
    </el-card>

    <el-dialog
      v-model="dialogVisible"
      :title="dialogTitle"
      width="640px"
      :close-on-click-modal="false"
    >
      <el-form label-position="top" @submit.prevent="saveRule">
        <el-alert
          v-if="isBuiltinEditing"
          title="内置规则仅允许调整说明、动作、替换文本、优先级、启用与告警开关。"
          type="info"
          :closable="false"
          show-icon
          class="form-alert"
        />

        <div class="form-grid">
          <el-form-item label="规则名称" required>
            <el-input
              v-model="form.name"
              data-test="dlp-name"
              maxlength="128"
              :disabled="isBuiltinEditing"
              placeholder="例如 项目代号泄露"
            />
          </el-form-item>
          <el-form-item label="分类">
            <el-select
              v-model="form.category"
              class="full-width"
              :disabled="isBuiltinEditing"
            >
              <el-option
                v-for="option in categoryOptions"
                :key="option.value"
                :label="option.label"
                :value="option.value"
              />
            </el-select>
          </el-form-item>
        </div>

        <el-form-item label="说明">
          <el-input
            v-model="form.description"
            maxlength="512"
            placeholder="描述该规则要保护的敏感信息"
          />
        </el-form-item>

        <div class="form-grid">
          <el-form-item label="匹配方式" required>
            <el-select
              v-model="form.match_type"
              class="full-width"
              :disabled="isBuiltinEditing"
            >
              <el-option label="关键词" value="keyword" />
              <el-option label="正则表达式" value="regex" />
            </el-select>
          </el-form-item>
          <el-form-item label="处置动作" required>
            <el-select v-model="form.action" class="full-width">
              <el-option label="阻断请求" value="block" />
              <el-option label="脱敏后转发" value="mask" />
              <el-option label="仅记录审计" value="audit" />
            </el-select>
          </el-form-item>
        </div>

        <el-form-item label="匹配内容" required>
          <el-input
            v-model="form.pattern"
            data-test="dlp-pattern"
            type="textarea"
            :rows="3"
            maxlength="4096"
            :disabled="isBuiltinEditing"
            :placeholder="
              form.match_type === 'keyword'
                ? '输入一个关键词，匹配时忽略大小写'
                : '输入 Go 正则表达式，例如 \\b1[3-9]\\d{9}\\b'
            "
          />
          <div class="field-hint">
            关键词规则按完整文本匹配且忽略大小写；需要匹配多个关键词时请使用批量导入。
          </div>
        </el-form-item>

        <div class="form-grid">
          <el-form-item label="替换文本">
            <el-input
              v-model="form.replacement"
              maxlength="128"
              placeholder="***"
            />
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

        <div class="form-grid">
          <el-form-item label="启用">
            <el-switch
              v-model="form.enabled"
              active-text="启用"
              inactive-text="禁用"
            />
          </el-form-item>
          <el-form-item label="命中后发送告警邮件">
            <el-switch
              v-model="form.alert_enabled"
              active-text="开启"
              inactive-text="关闭"
            />
          </el-form-item>
        </div>

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
          data-test="save-dlp-rule"
          type="primary"
          :loading="saving"
          @click="saveRule"
        >
          保存
        </el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="keywordDialogVisible"
      title="批量导入关键词"
      width="600px"
      :close-on-click-modal="false"
    >
      <el-form label-position="top" @submit.prevent="saveKeywords">
        <el-form-item label="规则名称" required>
          <el-input
            v-model="keywordForm.name"
            data-test="keyword-rule-name"
            maxlength="128"
            placeholder="例如 项目敏感关键词"
          />
        </el-form-item>

        <div class="form-grid">
          <el-form-item label="分类">
            <el-select
              v-model="keywordForm.category"
              class="full-width"
            >
              <el-option
                v-for="option in categoryOptions"
                :key="option.value"
                :label="option.label"
                :value="option.value"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="处置动作" required>
            <el-select v-model="keywordForm.action" class="full-width">
              <el-option label="阻断请求" value="block" />
              <el-option label="脱敏后转发" value="mask" />
              <el-option label="仅记录审计" value="audit" />
            </el-select>
          </el-form-item>
        </div>

        <el-form-item label="关键词列表" required>
          <el-input
            v-model="keywordForm.keywords"
            data-test="keyword-list"
            type="textarea"
            :rows="8"
            maxlength="20000"
            placeholder="每行一个关键词，例如：&#10;Project Falcon&#10;客户名单&#10;内部报价"
          />
          <div class="field-hint">
            每行一个关键词，系统会生成一条忽略大小写的匹配规则；重复关键词自动去重。
          </div>
        </el-form-item>

        <div class="form-grid">
          <el-form-item label="替换文本">
            <el-input
              v-model="keywordForm.replacement"
              maxlength="128"
              placeholder="***"
            />
          </el-form-item>
          <el-form-item label="优先级">
            <el-input-number
              v-model="keywordForm.priority"
              :min="0"
              :controls="false"
              class="full-width"
            />
          </el-form-item>
        </div>

        <div class="form-grid">
          <el-form-item label="启用">
            <el-switch
              v-model="keywordForm.enabled"
              active-text="启用"
              inactive-text="禁用"
            />
          </el-form-item>
          <el-form-item label="命中后发送告警邮件">
            <el-switch
              v-model="keywordForm.alert_enabled"
              active-text="开启"
              inactive-text="关闭"
            />
          </el-form-item>
        </div>

        <el-alert
          v-if="keywordError"
          :title="keywordError"
          type="error"
          :closable="false"
          show-icon
        />
      </el-form>

      <template #footer>
        <el-button @click="keywordDialogVisible = false">取消</el-button>
        <el-button
          data-test="save-keywords"
          type="primary"
          :loading="keywordSaving"
          @click="saveKeywords"
        >
          导入
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
  max-width: 760px;
  margin: 0;
  color: #8393a7;
  font-size: 14px;
  line-height: 1.6;
}

.heading-actions {
  display: flex;
  flex-shrink: 0;
  gap: 10px;
}

.table-card,
.test-card {
  border: 1px solid #e8edf4;
  border-radius: 12px;
}

.test-card {
  margin-top: 20px;
}

.toolbar {
  margin-bottom: 16px;
}

.load-error {
  margin-bottom: 16px;
}

.rule-name {
  display: flex;
  align-items: center;
  gap: 8px;
}

.rule-description {
  display: block;
  margin-top: 4px;
  color: #98a6b7;
  font-size: 12px;
  line-height: 1.5;
}

.test-heading h3 {
  margin: 0 0 6px;
  color: #1f2d3d;
  font-size: 16px;
}

.test-heading p {
  margin: 0;
  color: #98a6b7;
  font-size: 12px;
}

.test-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 20px;
  margin-top: 16px;
}

.test-actions {
  margin-top: 10px;
}

.test-output {
  min-height: 190px;
  padding: 16px;
  border: 1px solid #e8edf4;
  border-radius: 10px;
  background: #f8fafc;
}

.result-heading,
.masked-heading {
  display: flex;
  align-items: center;
  gap: 10px;
  color: #52637a;
  font-size: 13px;
  font-weight: 600;
}

.masked-heading {
  margin-top: 16px;
}

.no-hits,
.result-placeholder {
  margin-top: 14px;
  color: #98a6b7;
  font-size: 13px;
  line-height: 1.7;
}

.hit-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 12px;
}

.hit-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}

.hit-meta {
  color: #98a6b7;
  font-size: 12px;
}

.masked-text {
  max-height: 220px;
  margin: 10px 0 0;
  padding: 12px;
  overflow: auto;
  border-radius: 8px;
  background: #16212f;
  color: #d8e4f2;
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', monospace;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
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

@media (max-width: 900px) {
  .test-layout {
    grid-template-columns: 1fr;
  }
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
