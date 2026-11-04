<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete, Edit, Plus, Search } from '@element-plus/icons-vue'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  Department,
  DepartmentDeleteResult,
  DepartmentDirectory,
  DepartmentPayload,
  Employee,
  EmployeePayload,
  EmployeeQuota,
  EmployeeQuotaPayload,
  PageResponse,
  PortalAccountCredentialResult,
  PortalAccountStatus,
  QuotaPeriod,
  QuotaType,
} from '../api/types'
import { useAuthStore } from '../stores/auth'
import { formatDateTime } from '../utils/datetime'

interface EmployeeForm {
  employee_no: string
  name: string
  email: string
  department_id: number | null
  status: number
}

type DepartmentSelection = 'all' | 'unassigned' | number

const auth = useAuthStore()
const canManage = computed(
  () => auth.isSuperAdmin || auth.user?.role === 'operator',
)

const employees = ref<Employee[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const loadError = ref('')

const searchQuery = ref('')
const statusFilter = ref<number | ''>('')
const selectedDepartment = ref<DepartmentSelection>('all')

const departments = ref<Department[]>([])
const departmentTotal = ref(0)
const unassignedCount = ref(0)
const directoryError = ref('')

const dialogVisible = ref(false)
const editing = ref<Employee | null>(null)
const saving = ref(false)
const formError = ref('')
const form = reactive<EmployeeForm>({
  employee_no: '',
  name: '',
  email: '',
  department_id: null,
  status: 1,
})

const dialogTitle = computed(() => (editing.value ? '编辑员工' : '新增员工'))

const deptDialogVisible = ref(false)
const deptEditing = ref<Department | null>(null)
const deptName = ref('')
const deptSaving = ref(false)
const deptFormError = ref('')
const deptDialogTitle = computed(() =>
  deptEditing.value ? '重命名部门' : '新建部门',
)

const deptDeleteVisible = ref(false)
const deptDeleteTarget = ref<Department | null>(null)
const deptMoveMode = ref<'none' | number>('none')
const deptDeleting = ref(false)
const deptDeleteError = ref('')

const moveTargets = computed(() =>
  departments.value.filter((item) => item.id !== deptDeleteTarget.value?.id),
)

const scopeLabel = computed(() => {
  if (selectedDepartment.value === 'all') return '全部员工'
  if (selectedDepartment.value === 'unassigned') return '未分配'
  return (
    departments.value.find((item) => item.id === selectedDepartment.value)
      ?.name ?? '全部员工'
  )
})

const numberFormat = new Intl.NumberFormat('zh-CN')

const quotaVisible = ref(false)
const quotaTarget = ref<Employee | null>(null)
const quotaInfo = ref<EmployeeQuota | null>(null)
const quotaLoading = ref(false)
const quotaSaving = ref(false)
const quotaError = ref('')
const quotaForm = reactive({
  quotaType: 'tokens' as QuotaType,
  quotaPeriod: 'monthly' as QuotaPeriod,
  quotaTotal: 0,
})

const quotaTitle = computed(() =>
  quotaTarget.value
    ? `员工额度 · ${quotaTarget.value.name}（${quotaTarget.value.employee_no}）`
    : '员工额度',
)

function quotaTypeLabel(type: QuotaType | null): string {
  return type === 'requests' ? '请求数' : 'Tokens'
}

function quotaPeriodLabel(period: QuotaPeriod | null): string {
  switch (period) {
    case 'daily':
      return '每日'
    case 'monthly':
      return '每月'
    case 'total':
      return '总量'
    default:
      return '未设置'
  }
}

const portalVisible = ref(false)
const portalTarget = ref<Employee | null>(null)
const portalLoading = ref(false)
const portalBusy = ref(false)
const portalError = ref('')
const portalStatus = ref<PortalAccountStatus | null>(null)
const portalPassword = ref('')

const portalTitle = computed(() =>
  portalTarget.value
    ? `门户账号 · ${portalTarget.value.name}（${portalTarget.value.employee_no}）`
    : '门户账号',
)

function portalPath(): string {
  return `/employees/${portalTarget.value?.id}/portal-account`
}

async function openPortal(employee: Employee): Promise<void> {
  portalTarget.value = employee
  portalVisible.value = true
  portalPassword.value = ''
  portalError.value = ''
  await loadPortalStatus()
}

async function loadPortalStatus(): Promise<void> {
  if (!portalTarget.value) return
  portalLoading.value = true
  portalError.value = ''
  try {
    portalStatus.value = await api.get<PortalAccountStatus>(portalPath())
  } catch (error) {
    portalError.value = apiErrorMessage(error, '门户账号状态加载失败')
  } finally {
    portalLoading.value = false
  }
}

async function createPortalAccount(): Promise<void> {
  if (!portalTarget.value) return
  portalBusy.value = true
  portalError.value = ''
  try {
    const result = await api.post<PortalAccountCredentialResult>(portalPath())
    portalPassword.value = result.initial_password
    await loadPortalStatus()
  } catch (error) {
    portalError.value = apiErrorMessage(error, '门户账号创建失败')
  } finally {
    portalBusy.value = false
  }
}

async function resetPortalPassword(): Promise<void> {
  if (!portalTarget.value) return
  try {
    await ElMessageBox.confirm(
      '重置后该员工的现有门户登录会立即失效，并需要使用新密码重新登录。',
      '确认重置门户密码',
      {
        type: 'warning',
        confirmButtonText: '确认重置',
        cancelButtonText: '取消',
      },
    )
  } catch {
    return
  }
  portalBusy.value = true
  portalError.value = ''
  try {
    const result = await api.post<PortalAccountCredentialResult>(
      `${portalPath()}/reset-password`,
    )
    portalPassword.value = result.initial_password
    await loadPortalStatus()
  } catch (error) {
    portalError.value = apiErrorMessage(error, '重置密码失败')
  } finally {
    portalBusy.value = false
  }
}

async function disablePortalAccount(): Promise<void> {
  if (!portalTarget.value) return
  try {
    await ElMessageBox.confirm(
      '停用后该员工将无法登录门户，现有会话也会失效。',
      '确认停用门户账号',
      {
        type: 'warning',
        confirmButtonText: '确认停用',
        cancelButtonText: '取消',
      },
    )
  } catch {
    return
  }
  portalBusy.value = true
  portalError.value = ''
  try {
    await api.del(portalPath())
    portalPassword.value = ''
    await loadPortalStatus()
    ElMessage.success('门户账号已停用')
  } catch (error) {
    portalError.value = apiErrorMessage(error, '停用门户账号失败')
  } finally {
    portalBusy.value = false
  }
}

async function copyPassword(): Promise<void> {
  try {
    await navigator.clipboard.writeText(portalPassword.value)
    ElMessage.success('初始密码已复制')
  } catch {
    ElMessage.warning('复制失败，请手动选择并复制')
  }
}

async function openQuota(employee: Employee): Promise<void> {
  quotaTarget.value = employee
  quotaInfo.value = null
  quotaError.value = ''
  quotaForm.quotaType = employee.quota_type ?? 'tokens'
  quotaForm.quotaPeriod = employee.quota_period ?? 'monthly'
  quotaForm.quotaTotal = employee.quota_total ?? 0
  quotaVisible.value = true
  await loadQuota(employee.id)
}

async function loadQuota(employeeId: string): Promise<void> {
  quotaLoading.value = true
  try {
    const current = await api.get<EmployeeQuota>(
      `/employees/${employeeId}/quota`,
    )
    quotaInfo.value = current
    quotaForm.quotaType = current.quota_type
    quotaForm.quotaPeriod = current.quota_period
    quotaForm.quotaTotal = current.quota_total
  } catch {
    // Newly created employees may not have a quota row yet; the edit form
    // falls back to the values projected on the employee row.
    quotaInfo.value = null
  } finally {
    quotaLoading.value = false
  }
}

async function saveQuota(): Promise<void> {
  if (!quotaTarget.value) return
  quotaError.value = ''
  if (
    !Number.isInteger(quotaForm.quotaTotal) ||
    quotaForm.quotaTotal < 0
  ) {
    quotaError.value = '额度总量必须是非负整数'
    return
  }
  const payload: EmployeeQuotaPayload = {
    quota_type: quotaForm.quotaType,
    quota_period: quotaForm.quotaPeriod,
    quota_total: quotaForm.quotaTotal,
  }
  quotaSaving.value = true
  try {
    const updated = await api.put<EmployeeQuota>(
      `/employees/${quotaTarget.value.id}/quota`,
      payload,
    )
    quotaInfo.value = updated
    ElMessage.success('员工额度已更新，立即生效')
    quotaVisible.value = false
    await loadEmployees()
  } catch (error) {
    quotaError.value = apiErrorMessage(error, '额度更新失败')
  } finally {
    quotaSaving.value = false
  }
}

async function loadDirectory(): Promise<void> {
  directoryError.value = ''
  try {
    const directory = await api.get<DepartmentDirectory>('/departments')
    departments.value = directory.items
    departmentTotal.value = directory.total_employees
    unassignedCount.value = directory.unassigned_count
    if (
      typeof selectedDepartment.value === 'number' &&
      !directory.items.some(
        (item) => item.id === selectedDepartment.value,
      )
    ) {
      selectedDepartment.value = 'all'
    }
  } catch (error) {
    directoryError.value = apiErrorMessage(error, '部门列表加载失败')
  }
}

async function loadEmployees(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const params = new URLSearchParams({
      page: String(page.value),
      page_size: String(pageSize.value),
    })
    const query = searchQuery.value.trim()
    if (query) params.set('q', query)
    if (selectedDepartment.value === 'unassigned') {
      params.set('unassigned', '1')
    } else if (typeof selectedDepartment.value === 'number') {
      params.set('department_id', String(selectedDepartment.value))
    }
    if (statusFilter.value !== '') {
      params.set('status', String(statusFilter.value))
    }
    const response = await api.get<PageResponse<Employee>>(
      `/employees?${params.toString()}`,
    )
    employees.value = response.items
    total.value = response.total
  } catch (error) {
    loadError.value = apiErrorMessage(error, '员工列表加载失败')
  } finally {
    loading.value = false
  }
}

function selectDepartment(selection: DepartmentSelection): void {
  selectedDepartment.value = selection
  search()
}

function search(): void {
  page.value = 1
  void loadEmployees()
}

function resetFilters(): void {
  searchQuery.value = ''
  statusFilter.value = ''
  selectedDepartment.value = 'all'
  page.value = 1
  void loadEmployees()
}

function openCreate(): void {
  editing.value = null
  formError.value = ''
  form.employee_no = ''
  form.name = ''
  form.email = ''
  form.department_id =
    typeof selectedDepartment.value === 'number'
      ? selectedDepartment.value
      : null
  form.status = 1
  dialogVisible.value = true
}

function openEdit(employee: Employee): void {
  editing.value = employee
  formError.value = ''
  form.employee_no = employee.employee_no
  form.name = employee.name
  form.email = employee.email
  form.department_id = employee.department_id ?? null
  form.status = employee.status
  dialogVisible.value = true
}

async function save(): Promise<void> {
  formError.value = ''
  const employeeNo = form.employee_no.trim()
  const name = form.name.trim()
  const email = form.email.trim()
  if (!employeeNo) {
    formError.value = '请输入工号'
    return
  }
  if (!name) {
    formError.value = '请输入姓名'
    return
  }
  if (email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
    formError.value = '邮箱格式不正确'
    return
  }

  const payload: EmployeePayload = {
    employee_no: employeeNo,
    name,
    email,
    department_id:
      typeof form.department_id === 'number' ? form.department_id : null,
    status: form.status,
  }

  saving.value = true
  try {
    if (editing.value) {
      await api.put<Employee>(`/employees/${editing.value.id}`, payload)
      ElMessage.success('员工信息已更新')
    } else {
      await api.post<Employee>('/employees', payload)
      ElMessage.success('员工已创建')
    }
    dialogVisible.value = false
    await Promise.all([loadDirectory(), loadEmployees()])
  } catch (error) {
    formError.value = apiErrorMessage(error)
  } finally {
    saving.value = false
  }
}

function openCreateDepartment(): void {
  deptEditing.value = null
  deptName.value = ''
  deptFormError.value = ''
  deptDialogVisible.value = true
}

function openRenameDepartment(department: Department): void {
  deptEditing.value = department
  deptName.value = department.name
  deptFormError.value = ''
  deptDialogVisible.value = true
}

async function saveDepartment(): Promise<void> {
  deptFormError.value = ''
  const name = deptName.value.trim()
  if (!name) {
    deptFormError.value = '请输入部门名称'
    return
  }
  if ([...name].length > 50) {
    deptFormError.value = '部门名称不能超过 50 个字符'
    return
  }
  const payload: DepartmentPayload = { name }
  deptSaving.value = true
  try {
    if (deptEditing.value) {
      await api.put<Department>(
        `/departments/${deptEditing.value.id}`,
        payload,
      )
      ElMessage.success('部门名称已更新')
    } else {
      const created = await api.post<Department>('/departments', payload)
      selectedDepartment.value = created.id
      ElMessage.success('部门已创建')
    }
    deptDialogVisible.value = false
    await Promise.all([loadDirectory(), loadEmployees()])
  } catch (error) {
    deptFormError.value = apiErrorMessage(error)
  } finally {
    deptSaving.value = false
  }
}

function openDeleteDepartment(department: Department): void {
  deptDeleteTarget.value = department
  deptMoveMode.value = 'none'
  deptDeleteError.value = ''
  deptDeleteVisible.value = true
}

async function deleteDepartment(): Promise<void> {
  const target = deptDeleteTarget.value
  if (!target) return
  deptDeleting.value = true
  deptDeleteError.value = ''
  try {
    const move =
      target.employee_count > 0
        ? `?move_to=${deptMoveMode.value === 'none' ? 'none' : deptMoveMode.value}`
        : ''
    const result = await api.del<DepartmentDeleteResult>(
      `/departments/${target.id}${move}`,
    )
    deptDeleteVisible.value = false
    if (typeof selectedDepartment.value === 'number' && selectedDepartment.value === target.id) {
      selectedDepartment.value = 'all'
    }
    ElMessage.success(
      result.moved_employees > 0
        ? `部门已删除，${result.moved_employees} 名员工已转移`
        : '部门已删除',
    )
    await Promise.all([loadDirectory(), loadEmployees()])
  } catch (error) {
    deptDeleteError.value = apiErrorMessage(error, '删除部门失败')
  } finally {
    deptDeleting.value = false
  }
}

onMounted(() => {
  void loadDirectory()
  void loadEmployees()
})
</script>

<template>
  <div class="employees-page">
    <section class="page-heading">
      <div>
        <h2>团队管理</h2>
        <p>维护部门与员工档案。共享额度按员工独立计算，员工的所有 Key 共享扣减。</p>
      </div>
      <el-button
        v-if="canManage"
        data-test="create-employee"
        type="primary"
        :icon="Plus"
        @click="openCreate"
      >
        新增员工
      </el-button>
    </section>

    <div class="team-layout">
      <aside class="department-panel">
        <div class="panel-heading">
          <div>
            <h3>部门</h3>
            <span v-if="!directoryError">
              {{ departmentTotal }} 名员工 · {{ departments.length }} 个部门
            </span>
            <span v-else>部门列表加载失败</span>
          </div>
          <el-tooltip v-if="canManage" content="新建部门" placement="top">
            <el-button
              text
              circle
              data-test="create-department"
              :icon="Plus"
              @click="openCreateDepartment"
            />
          </el-tooltip>
        </div>

        <el-alert
          v-if="directoryError"
          :title="directoryError"
          type="error"
          :closable="false"
          show-icon
          class="directory-error"
        />

        <ul class="department-list">
          <li
            :class="{ active: selectedDepartment === 'all' }"
            data-test="department-all"
            @click="selectDepartment('all')"
          >
            <span class="department-name">全部员工</span>
            <span class="department-count">{{ departmentTotal }}</span>
          </li>
          <li
            :class="{ active: selectedDepartment === 'unassigned' }"
            data-test="department-unassigned"
            @click="selectDepartment('unassigned')"
          >
            <span class="department-name">未分配</span>
            <span class="department-count">{{ unassignedCount }}</span>
          </li>
          <li
            v-for="department in departments"
            :key="department.id"
            :class="{ active: selectedDepartment === department.id }"
            :data-test="`department-${department.id}`"
            @click="selectDepartment(department.id)"
          >
            <span class="department-name" :title="department.name">
              {{ department.name }}
            </span>
            <span class="department-count">
              {{ department.employee_count }}
            </span>
            <span v-if="canManage" class="department-actions" @click.stop>
              <el-tooltip content="重命名" placement="top">
                <el-button
                  text
                  size="small"
                  :icon="Edit"
                  :data-test="`rename-department-${department.id}`"
                  @click="openRenameDepartment(department)"
                />
              </el-tooltip>
              <el-tooltip content="删除" placement="top">
                <el-button
                  text
                  size="small"
                  :icon="Delete"
                  :data-test="`delete-department-${department.id}`"
                  @click="openDeleteDepartment(department)"
                />
              </el-tooltip>
            </span>
          </li>
        </ul>

        <p v-if="departments.length === 0" class="department-empty">
          暂无部门，点击右上角 + 创建
        </p>
      </aside>

      <el-card class="table-card" shadow="never">
        <div class="toolbar">
          <el-input
            v-model="searchQuery"
            data-test="employee-search"
            class="search-input"
            clearable
            placeholder="搜索工号、姓名或邮箱"
            :prefix-icon="Search"
            @keyup.enter="search"
            @clear="search"
          />
          <el-select
            v-model="statusFilter"
            class="status-select"
            placeholder="全部状态"
            @change="search"
          >
            <el-option label="全部状态" value="" />
            <el-option label="启用" :value="1" />
            <el-option label="停用" :value="0" />
          </el-select>
          <el-button data-test="employee-search-button" @click="search">
            查询
          </el-button>
          <el-button @click="resetFilters">重置</el-button>
          <span class="scope-label">{{ scopeLabel }}</span>
        </div>

        <el-alert
          v-if="loadError"
          :title="loadError"
          type="error"
          :closable="false"
          show-icon
          class="load-error"
        />

        <el-table v-loading="loading" :data="employees" stripe>
          <el-table-column prop="employee_no" label="工号" min-width="100" />
          <el-table-column prop="name" label="姓名" min-width="100" />
          <el-table-column
            prop="email"
            label="邮箱"
            min-width="140"
            show-overflow-tooltip
          >
            <template #default="{ row }">{{ row.email || '—' }}</template>
          </el-table-column>
          <el-table-column label="部门" min-width="110" show-overflow-tooltip>
            <template #default="{ row }">
              <span :class="{ 'muted-cell': !row.department_name }">
                {{ row.department_name || '未分配' }}
              </span>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="80">
            <template #default="{ row }">
              <el-tag :type="row.status === 1 ? 'success' : 'info'" size="small">
                {{ row.status === 1 ? '启用' : '停用' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="个人额度" min-width="140">
            <template #default="{ row }">
              <div class="quota-cell">
                <span class="quota-number">
                  {{ numberFormat.format(row.quota_used) }} /
                  {{ row.quota_total === null ? '未设置' : numberFormat.format(row.quota_total) }}
                </span>
                <span class="quota-meta">
                  {{ quotaTypeLabel(row.quota_type) }} ·
                  {{ quotaPeriodLabel(row.quota_period) }}
                </span>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="创建时间" width="170" show-overflow-tooltip>
            <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
          </el-table-column>
          <el-table-column v-if="canManage" label="操作" width="200" fixed="right">
            <template #default="{ row }">
              <div class="row-actions">
                <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
                <el-button link type="primary" @click="openQuota(row)">额度</el-button>
                <el-button link type="primary" @click="openPortal(row)">
                  门户账号
                </el-button>
              </div>
            </template>
          </el-table-column>
          <template #empty>
            <el-empty description="暂无员工数据" :image-size="80" />
          </template>
        </el-table>

        <div class="pagination-row">
          <el-pagination
            v-model:current-page="page"
            v-model:page-size="pageSize"
            :total="total"
            layout="total, prev, pager, next"
            background
            @current-change="loadEmployees"
          />
        </div>
      </el-card>
    </div>

    <el-dialog
      v-model="dialogVisible"
      :title="dialogTitle"
      width="560px"
      :close-on-click-modal="false"
    >
      <el-form label-position="top" @submit.prevent="save">
        <div class="form-grid">
          <el-form-item label="工号" required>
            <el-input
              v-model="form.employee_no"
              data-test="employee-no"
              maxlength="64"
              placeholder="例如 E001"
            />
          </el-form-item>
          <el-form-item label="姓名" required>
            <el-input
              v-model="form.name"
              data-test="employee-name"
              maxlength="128"
              placeholder="员工姓名"
            />
          </el-form-item>
        </div>
        <el-form-item label="邮箱">
          <el-input
            v-model="form.email"
            data-test="employee-email"
            maxlength="255"
            placeholder="name@example.com"
          />
        </el-form-item>
        <div class="form-grid">
          <el-form-item label="部门">
            <el-select
              v-model="form.department_id"
              clearable
              placeholder="未分配"
              class="full-width"
              data-test="employee-department"
            >
              <el-option
                v-for="department in departments"
                :key="department.id"
                :label="department.name"
                :value="department.id"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="状态">
            <el-switch
              v-model="form.status"
              :active-value="1"
              :inactive-value="0"
              active-text="启用"
              inactive-text="停用"
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
          data-test="save-employee"
          type="primary"
          :loading="saving"
          @click="save"
        >
          保存
        </el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="deptDialogVisible"
      :title="deptDialogTitle"
      width="420px"
      :close-on-click-modal="false"
    >
      <el-form label-position="top" @submit.prevent="saveDepartment">
        <el-form-item label="部门名称" required>
          <el-input
            v-model="deptName"
            data-test="department-name"
            maxlength="50"
            show-word-limit
            placeholder="例如 研发部"
            @keyup.enter="saveDepartment"
          />
        </el-form-item>
        <el-alert
          v-if="deptFormError"
          :title="deptFormError"
          type="error"
          :closable="false"
          show-icon
        />
      </el-form>

      <template #footer>
        <el-button @click="deptDialogVisible = false">取消</el-button>
        <el-button
          data-test="save-department"
          type="primary"
          :loading="deptSaving"
          @click="saveDepartment"
        >
          保存
        </el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="deptDeleteVisible"
      title="删除部门"
      width="460px"
      :close-on-click-modal="false"
    >
      <div v-if="deptDeleteTarget" class="delete-body">
        <p>
          确认删除部门「{{ deptDeleteTarget.name }}」？
        </p>
        <template v-if="deptDeleteTarget.employee_count > 0">
          <p class="delete-hint">
            该部门下还有 {{ deptDeleteTarget.employee_count }} 名员工，删除前请选择转移去向。
          </p>
          <el-select v-model="deptMoveMode" class="full-width">
            <el-option label="未分配（不设部门）" value="none" />
            <el-option
              v-for="department in moveTargets"
              :key="department.id"
              :label="department.name"
              :value="department.id"
            />
          </el-select>
        </template>
        <el-alert
          v-if="deptDeleteError"
          :title="deptDeleteError"
          type="error"
          :closable="false"
          show-icon
        />
      </div>

      <template #footer>
        <el-button @click="deptDeleteVisible = false">取消</el-button>
        <el-button
          data-test="confirm-delete-department"
          type="danger"
          :loading="deptDeleting"
          @click="deleteDepartment"
        >
          {{ deptDeleteTarget?.employee_count ? '转移并删除' : '删除' }}
        </el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="quotaVisible"
      :title="quotaTitle"
      width="540px"
      :close-on-click-modal="false"
    >
      <div v-loading="quotaLoading" class="quota-dialog">
        <div v-if="quotaInfo" class="quota-summary">
          <div>
            <span class="quota-summary-label">当前周期已用</span>
            <strong>
              {{ numberFormat.format(quotaInfo.quota_used) }} /
              {{ numberFormat.format(quotaInfo.quota_total) }}
            </strong>
          </div>
          <div>
            <span class="quota-summary-label">剩余</span>
            <strong>{{ numberFormat.format(quotaInfo.quota_remaining) }}</strong>
          </div>
          <div>
            <span class="quota-summary-label">下次重置</span>
            <strong>
              {{ quotaInfo.quota_period === 'total' ? '不自动重置' : formatDateTime(quotaInfo.resets_at) }}
            </strong>
          </div>
        </div>

        <el-form label-position="top" @submit.prevent="saveQuota">
          <div class="form-grid">
            <el-form-item label="额度类型">
              <el-select v-model="quotaForm.quotaType" class="full-width">
                <el-option label="Token 数" value="tokens" />
                <el-option label="请求数" value="requests" />
              </el-select>
            </el-form-item>
            <el-form-item label="额度周期">
              <el-select v-model="quotaForm.quotaPeriod" class="full-width">
                <el-option label="每日" value="daily" />
                <el-option label="每月" value="monthly" />
                <el-option label="总量（不自动重置）" value="total" />
              </el-select>
            </el-form-item>
          </div>
          <el-form-item label="额度总量" required>
            <el-input-number
              v-model="quotaForm.quotaTotal"
              :min="0"
              :controls="false"
              class="full-width"
            />
            <div class="quota-hint">
              员工的所有 Key 共享该额度。更改周期会重置本期已用额度。
            </div>
          </el-form-item>

          <el-alert
            v-if="quotaError"
            :title="quotaError"
            type="error"
            :closable="false"
            show-icon
          />
        </el-form>
      </div>

      <template #footer>
        <el-button @click="quotaVisible = false">取消</el-button>
        <el-button type="primary" :loading="quotaSaving" @click="saveQuota">
          保存额度
        </el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="portalVisible"
      :title="portalTitle"
      width="560px"
      :close-on-click-modal="false"
    >
      <div v-loading="portalLoading" class="portal-dialog">
        <el-alert
          v-if="portalPassword"
          type="warning"
          :closable="false"
          show-icon
          title="请立即将初始密码交给员工"
          description="该密码只展示这一次。员工首次登录后必须修改密码，管理员无法再次查看。"
        />

        <div v-if="portalPassword" class="password-box">
          <code>{{ portalPassword }}</code>
          <el-button size="small" @click="copyPassword">复制</el-button>
        </div>

        <el-alert
          v-if="portalError"
          :title="portalError"
          type="error"
          :closable="false"
          show-icon
        />

        <div v-if="portalStatus" class="status-block">
          <el-tag :type="portalStatus.exists ? 'success' : 'info'" size="small">
            {{ portalStatus.exists ? '门户账号已开通' : '尚未开通门户账号' }}
          </el-tag>
          <p v-if="portalStatus.exists" class="status-meta">
            首次登录需修改密码：{{ portalStatus.must_change_password ? '是' : '否' }}
            <span v-if="portalStatus.last_login_at">
              · 最近登录 {{ formatDateTime(portalStatus.last_login_at) }}
            </span>
          </p>
          <p v-else class="status-meta">
            开通后员工可使用工号与初始密码登录员工门户。
          </p>
        </div>

        <div class="portal-actions">
          <el-button
            v-if="!portalStatus?.exists"
            type="primary"
            :loading="portalBusy"
            @click="createPortalAccount"
          >
            开通门户账号
          </el-button>
          <template v-else>
            <el-button :loading="portalBusy" @click="resetPortalPassword">
              重置密码
            </el-button>
            <el-button
              type="danger"
              plain
              :loading="portalBusy"
              @click="disablePortalAccount"
            >
              停用账号
            </el-button>
          </template>
        </div>
      </div>

      <template #footer>
        <el-button @click="portalVisible = false">关闭</el-button>
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
  margin: 0;
  color: var(--gw-text-muted);
  font-size: 13px;
}

.team-layout {
  display: grid;
  grid-template-columns: 232px minmax(0, 1fr);
  align-items: start;
  gap: 16px;
}

.department-panel {
  position: sticky;
  top: 16px;
  padding: 16px 14px;
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  background: #fff;
  box-shadow: var(--gw-shadow-card);
}

.panel-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 12px;
  padding: 0 4px;
}

.panel-heading h3 {
  margin: 0 0 4px;
  color: var(--gw-text);
  font-size: 14px;
  font-weight: 600;
}

.panel-heading span {
  color: var(--gw-text-muted);
  font-size: 11px;
}

.department-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.department-list li {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: 8px;
  color: var(--gw-text-secondary);
  font-size: 13px;
  cursor: pointer;
  transition: background-color 0.15s ease;
}

.department-list li:hover {
  background: #f4f7fb;
}

.department-list li.active {
  background: rgba(47, 107, 255, 0.08);
  color: var(--gw-primary);
  font-weight: 600;
}

.department-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.department-count {
  color: var(--gw-text-muted);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.department-list li.active .department-count {
  color: var(--gw-primary);
}

.department-actions {
  display: none;
  align-items: center;
  gap: 0;
  margin-right: -4px;
}

.department-list li:hover .department-actions {
  display: flex;
}

.department-actions :deep(.el-button) {
  padding: 2px;
  color: var(--gw-text-muted);
}

.department-actions :deep(.el-button:hover) {
  color: var(--gw-primary);
}

.department-empty {
  margin: 10px 4px 2px;
  color: var(--gw-text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.directory-error {
  margin-bottom: 10px;
}

.table-card {
  border: 1px solid var(--gw-border);
  border-radius: var(--gw-radius-lg);
  box-shadow: var(--gw-shadow-card);
}

.table-card :deep(.el-card__body) {
  padding: 22px 22px 18px;
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin-bottom: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid #f0f4f9;
}

.search-input {
  width: 280px;
}

.status-select {
  width: 130px;
}

.scope-label {
  margin-left: auto;
  padding: 3px 10px;
  border-radius: 999px;
  background: #f4f7fb;
  color: #8694a7;
  font-size: 11px;
  white-space: nowrap;
}

.load-error {
  margin-bottom: 16px;
}

.muted-cell {
  color: var(--gw-text-muted);
}

.quota-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.quota-number {
  color: #26384d;
  font-size: 12px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.quota-meta {
  color: #9aa7b8;
  font-size: 10px;
}

.quota-dialog {
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-height: 80px;
}

.quota-summary {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 14px;
  padding: 12px 14px;
  border: 1px solid #eaf0f8;
  border-radius: 10px;
  background: #f8faff;
}

.quota-summary > div {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.quota-summary-label {
  color: #93a0b2;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.04em;
}

.quota-summary strong {
  color: #26384d;
  font-size: 13px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}

.quota-hint {
  margin-top: 5px;
  color: #9aa7b8;
  font-size: 11px;
  line-height: 1.5;
}

.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 18px;
}

.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

.full-width {
  width: 100%;
}

.row-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  white-space: nowrap;
}

.row-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}

.delete-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.delete-body p {
  margin: 0;
  color: var(--gw-text);
  font-size: 14px;
}

.delete-body .delete-hint {
  color: var(--gw-text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.portal-dialog {
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-height: 110px;
}

.password-box {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border: 1px dashed #e3c37a;
  border-radius: 10px;
  background: #fffaf0;
}

.password-box code {
  flex: 1;
  color: #7a4b00;
  font-family: ui-monospace, Consolas, monospace;
  font-size: 15px;
  letter-spacing: 0.06em;
  overflow-wrap: anywhere;
}

.status-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.status-meta {
  margin: 0;
  color: var(--gw-text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.portal-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 4px;
}

@media (max-width: 960px) {
  .team-layout {
    grid-template-columns: minmax(0, 1fr);
  }

  .department-panel {
    position: static;
  }

  .department-list {
    flex-direction: row;
    flex-wrap: wrap;
  }

  .department-list li {
    flex: 0 1 auto;
  }

  .scope-label {
    margin-left: 0;
  }
}

@media (max-width: 640px) {
  .page-heading {
    flex-direction: column;
  }

  .search-input,
  .status-select {
    width: 100%;
  }

  .form-grid {
    grid-template-columns: 1fr;
    gap: 0;
  }
}
</style>
