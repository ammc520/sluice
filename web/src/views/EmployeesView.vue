<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Plus, Search } from '@element-plus/icons-vue'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type { Employee, EmployeePayload, PageResponse } from '../api/types'
import { useAuthStore } from '../stores/auth'
import { formatDateTime } from '../utils/datetime'

interface EmployeeForm {
  employee_no: string
  name: string
  email: string
  department_id: number | undefined
  status: number
}

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
const departmentFilter = ref<number | ''>('')
const departmentOptions = ref<number[]>([])

const dialogVisible = ref(false)
const editing = ref<Employee | null>(null)
const saving = ref(false)
const formError = ref('')
const form = reactive<EmployeeForm>({
  employee_no: '',
  name: '',
  email: '',
  department_id: undefined,
  status: 1,
})

const dialogTitle = computed(() => (editing.value ? '编辑员工' : '新增员工'))

function mergeDepartmentOptions(items: Employee[]): void {
  const ids = new Set(departmentOptions.value)
  for (const employee of items) {
    if (employee.department_id) ids.add(employee.department_id)
  }
  departmentOptions.value = [...ids].sort((a, b) => a - b)
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
    if (departmentFilter.value !== '') {
      params.set('department_id', String(departmentFilter.value))
    }
    const response = await api.get<PageResponse<Employee>>(
      `/employees?${params.toString()}`,
    )
    employees.value = response.items
    total.value = response.total
    mergeDepartmentOptions(response.items)
  } catch (error) {
    loadError.value = apiErrorMessage(error, '员工列表加载失败')
  } finally {
    loading.value = false
  }
}

function search(): void {
  page.value = 1
  void loadEmployees()
}

function resetFilters(): void {
  searchQuery.value = ''
  departmentFilter.value = ''
  page.value = 1
  void loadEmployees()
}

function openCreate(): void {
  editing.value = null
  formError.value = ''
  form.employee_no = ''
  form.name = ''
  form.email = ''
  form.department_id = undefined
  form.status = 1
  dialogVisible.value = true
}

function openEdit(employee: Employee): void {
  editing.value = employee
  formError.value = ''
  form.employee_no = employee.employee_no
  form.name = employee.name
  form.email = employee.email
  form.department_id = employee.department_id ?? undefined
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
    department_id: form.department_id ?? null,
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
    await loadEmployees()
  } catch (error) {
    formError.value = apiErrorMessage(error)
  } finally {
    saving.value = false
  }
}

onMounted(loadEmployees)
</script>

<template>
  <div class="employees-page">
    <section class="page-heading">
      <div>
        <h2>员工管理</h2>
        <p>维护员工档案与账号状态，Token 额度按员工独立计算。</p>
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
          v-if="departmentOptions.length > 0"
          v-model="departmentFilter"
          class="department-select"
          clearable
          placeholder="全部部门"
          @change="search"
        >
          <el-option
            v-for="id in departmentOptions"
            :key="id"
            :label="`部门 #${id}`"
            :value="id"
          />
        </el-select>
        <el-input
          v-else
          class="department-select"
          disabled
          placeholder="暂无部门"
        />
        <el-button data-test="employee-search-button" @click="search">
          查询
        </el-button>
        <el-button @click="resetFilters">重置</el-button>
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
        <el-table-column prop="employee_no" label="工号" min-width="110" />
        <el-table-column prop="name" label="姓名" min-width="110" />
        <el-table-column prop="email" label="邮箱" min-width="190">
          <template #default="{ row }">{{ row.email || '—' }}</template>
        </el-table-column>
        <el-table-column label="部门" width="110">
          <template #default="{ row }">
            {{ row.department_id ? `部门 #${row.department_id}` : '—' }}
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.status === 1 ? 'success' : 'info'" size="small">
              {{ row.status === 1 ? '启用' : '停用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="160">
          <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column v-if="canManage" label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
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
          <el-form-item label="部门 ID">
            <el-input-number
              v-model="form.department_id"
              :min="1"
              :controls="false"
              placeholder="可选"
              class="full-width"
            />
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
  gap: 10px;
  margin-bottom: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid #f0f4f9;
}

.search-input {
  width: 300px;
}

.department-select {
  width: 160px;
}

.load-error {
  margin-bottom: 16px;
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

@media (max-width: 640px) {
  .page-heading {
    flex-direction: column;
  }

  .search-input,
  .department-select {
    width: 100%;
  }

  .form-grid {
    grid-template-columns: 1fr;
    gap: 0;
  }
}
</style>
