<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'

import { api } from '../api/client'
import { apiErrorMessage } from '../api/errors'
import type {
  AdminRole,
  AdminUser,
  AdminUserCreatePayload,
  AdminUserListResponse,
  AdminUserUpdatePayload,
  ResetPasswordResult,
} from '../api/types'
import { useAuthStore } from '../stores/auth'
import { formatDateTime } from '../utils/datetime'

interface CreateForm {
  username: string
  password: string
  role: AdminRole
  status: number
}

interface EditForm {
  username: string
  role: AdminRole
  status: number
}

interface ResetForm {
  password: string
  confirm: string
}

const auth = useAuthStore()
const router = useRouter()

const users = ref<AdminUser[]>([])
const loading = ref(false)
const loadError = ref('')

const createVisible = ref(false)
const createSaving = ref(false)
const createError = ref('')
const createForm = ref<CreateForm>({
  username: '',
  password: '',
  role: 'operator',
  status: 1,
})

const editVisible = ref(false)
const editingUser = ref<AdminUser | null>(null)
const editSaving = ref(false)
const editError = ref('')
const editForm = ref<EditForm>({
  username: '',
  role: 'operator',
  status: 1,
})

const resetVisible = ref(false)
const resettingUser = ref<AdminUser | null>(null)
const resetSaving = ref(false)
const resetError = ref('')
const resetForm = ref<ResetForm>({ password: '', confirm: '' })

const roleLabels: Record<AdminRole, string> = {
  super_admin: '超级管理员',
  operator: '操作员',
  auditor: '审计员',
}

const roleTagTypes: Record<AdminRole, 'danger' | 'warning' | 'info'> = {
  super_admin: 'danger',
  operator: 'warning',
  auditor: 'info',
}

function roleLabel(role: AdminRole): string {
  return roleLabels[role] ?? role
}

function roleTagType(role: AdminRole): 'danger' | 'warning' | 'info' {
  return roleTagTypes[role] ?? 'info'
}

function isSelf(user: AdminUser): boolean {
  return auth.user?.id === user.id
}

async function loadUsers(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const response = await api.get<AdminUserListResponse>('/admin-users')
    users.value = response.items
  } catch (error) {
    loadError.value = apiErrorMessage(error, '管理员列表加载失败')
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  createError.value = ''
  createForm.value = {
    username: '',
    password: '',
    role: 'operator',
    status: 1,
  }
  createVisible.value = true
}

function validUsername(username: string): boolean {
  return /^[A-Za-z0-9._-]{3,64}$/.test(username)
}

async function createUser(): Promise<void> {
  createError.value = ''
  const username = createForm.value.username.trim()
  if (!validUsername(username)) {
    createError.value = '用户名需为 3-64 位字母、数字、点、下划线或连字符'
    return
  }
  if (createForm.value.password.length < 12) {
    createError.value = '密码至少需要 12 个字符'
    return
  }

  const payload: AdminUserCreatePayload = {
    username,
    password: createForm.value.password,
    role: createForm.value.role,
    status: createForm.value.status,
  }
  createSaving.value = true
  try {
    await api.post<AdminUser>('/admin-users', payload)
    ElMessage.success('管理员账号已创建')
    createVisible.value = false
    await loadUsers()
  } catch (error) {
    createError.value = apiErrorMessage(error)
  } finally {
    createSaving.value = false
  }
}

function openEdit(user: AdminUser): void {
  editingUser.value = user
  editError.value = ''
  editForm.value = {
    username: user.username,
    role: user.role,
    status: user.status,
  }
  editVisible.value = true
}

async function saveEdit(): Promise<void> {
  const user = editingUser.value
  if (!user) return
  editError.value = ''
  const username = editForm.value.username.trim()
  if (!validUsername(username)) {
    editError.value = '用户名需为 3-64 位字母、数字、点、下划线或连字符'
    return
  }

  const disabling = user.status === 1 && editForm.value.status === 0
  if (disabling) {
    try {
      await ElMessageBox.confirm(
        `禁用「${user.username}」后，该账号的所有后台会话会立即失效。`,
        '确认禁用管理员',
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

  const payload: AdminUserUpdatePayload = {
    username,
    role: editForm.value.role,
    status: editForm.value.status,
  }
  editSaving.value = true
  try {
    await api.put<AdminUser>(`/admin-users/${user.id}`, payload)
    ElMessage.success('管理员账号已更新')
    editVisible.value = false
    if (isSelf(user)) {
      if (editForm.value.status === 0) {
        auth.clear()
        await router.replace('/login')
        return
      }
      await auth.me()
      if (!auth.isSuperAdmin) {
        await router.replace('/dashboard')
        return
      }
    }
    await loadUsers()
  } catch (error) {
    editError.value = apiErrorMessage(error)
  } finally {
    editSaving.value = false
  }
}

async function toggleStatus(user: AdminUser): Promise<void> {
  const nextStatus = user.status === 1 ? 0 : 1
  if (nextStatus === 0) {
    try {
      await ElMessageBox.confirm(
        `禁用「${user.username}」后，该账号的所有后台会话会立即失效。`,
        '确认禁用管理员',
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
    await api.put<AdminUser>(`/admin-users/${user.id}`, {
      status: nextStatus,
    })
    if (isSelf(user) && nextStatus === 0) {
      auth.clear()
      await router.replace('/login')
      return
    }
    ElMessage.success(nextStatus === 0 ? '管理员已禁用' : '管理员已启用')
    await loadUsers()
  } catch (error) {
    ElMessage.error(apiErrorMessage(error))
  }
}

function openReset(user: AdminUser): void {
  resettingUser.value = user
  resetError.value = ''
  resetForm.value = { password: '', confirm: '' }
  resetVisible.value = true
}

async function resetPassword(): Promise<void> {
  const user = resettingUser.value
  if (!user) return
  resetError.value = ''
  if (resetForm.value.password.length < 12) {
    resetError.value = '密码至少需要 12 个字符'
    return
  }
  if (resetForm.value.password !== resetForm.value.confirm) {
    resetError.value = '两次输入的密码不一致'
    return
  }

  resetSaving.value = true
  try {
    await api.post<ResetPasswordResult>(
      `/admin-users/${user.id}/reset-password`,
      { password: resetForm.value.password },
    )
    resetVisible.value = false
    ElMessage.success('密码已重置，该账号的现有会话已失效')
    if (isSelf(user)) {
      auth.clear()
      await router.replace('/login')
    }
  } catch (error) {
    resetError.value = apiErrorMessage(error)
  } finally {
    resetSaving.value = false
  }
}

onMounted(loadUsers)
</script>

<template>
  <div class="admin-users-page">
    <section class="page-heading">
      <div>
        <h2>管理员账号</h2>
        <p>管理后台账号、角色与密码。仅超级管理员可访问此页面。</p>
      </div>
      <el-button
        data-test="create-admin"
        type="primary"
        :icon="Plus"
        @click="openCreate"
      >
        新增管理员
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

      <el-table v-loading="loading" :data="users" stripe>
        <el-table-column label="用户名" min-width="150">
          <template #default="{ row }">
            <div class="username-cell">
              <strong>{{ row.username }}</strong>
              <el-tag v-if="isSelf(row)" size="small" effect="plain">
                当前账号
              </el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="角色" width="130">
          <template #default="{ row }">
            <el-tag :type="roleTagType(row.role)" size="small">
              {{ roleLabel(row.role) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.status === 1 ? 'success' : 'info'" size="small">
              {{ row.status === 1 ? '启用' : '禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="最后登录" width="160">
          <template #default="{ row }">
            {{ formatDateTime(row.last_login_at) }}
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="160">
          <template #default="{ row }">
            {{ formatDateTime(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="240" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openEdit(row)">
              编辑
            </el-button>
            <el-button link @click="toggleStatus(row)">
              {{ row.status === 1 ? '禁用' : '启用' }}
            </el-button>
            <el-button link type="warning" @click="openReset(row)">
              重置密码
            </el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无管理员账号" :image-size="80" />
        </template>
      </el-table>
    </el-card>

    <el-dialog
      v-model="createVisible"
      title="新增管理员"
      width="520px"
      :close-on-click-modal="false"
    >
      <el-form label-position="top" @submit.prevent="createUser">
        <el-form-item label="用户名" required>
          <el-input
            v-model="createForm.username"
            data-test="admin-username"
            maxlength="64"
            autocomplete="off"
            placeholder="3-64 位字母、数字、点、下划线或连字符"
          />
        </el-form-item>
        <el-form-item label="初始密码" required>
          <el-input
            v-model="createForm.password"
            data-test="admin-password"
            type="password"
            show-password
            autocomplete="new-password"
            maxlength="128"
            placeholder="至少 12 个字符"
          />
        </el-form-item>
        <div class="form-grid">
          <el-form-item label="角色" required>
            <el-select v-model="createForm.role" class="full-width">
              <el-option label="超级管理员" value="super_admin" />
              <el-option label="操作员" value="operator" />
              <el-option label="审计员" value="auditor" />
            </el-select>
          </el-form-item>
          <el-form-item label="状态">
            <el-switch
              v-model="createForm.status"
              :active-value="1"
              :inactive-value="0"
              active-text="启用"
              inactive-text="禁用"
            />
          </el-form-item>
        </div>

        <el-alert
          v-if="createError"
          :title="createError"
          type="error"
          :closable="false"
          show-icon
        />
      </el-form>

      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button
          data-test="save-admin"
          type="primary"
          :loading="createSaving"
          @click="createUser"
        >
          创建
        </el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="editVisible"
      title="编辑管理员"
      width="520px"
      :close-on-click-modal="false"
    >
      <el-form label-position="top" @submit.prevent="saveEdit">
        <el-form-item label="用户名" required>
          <el-input
            v-model="editForm.username"
            maxlength="64"
            autocomplete="off"
          />
        </el-form-item>
        <div class="form-grid">
          <el-form-item label="角色">
            <el-select v-model="editForm.role" class="full-width">
              <el-option label="超级管理员" value="super_admin" />
              <el-option label="操作员" value="operator" />
              <el-option label="审计员" value="auditor" />
            </el-select>
          </el-form-item>
          <el-form-item label="状态">
            <el-switch
              v-model="editForm.status"
              :active-value="1"
              :inactive-value="0"
              active-text="启用"
              inactive-text="禁用"
            />
          </el-form-item>
        </div>

        <el-alert
          v-if="editError"
          :title="editError"
          type="error"
          :closable="false"
          show-icon
        />
      </el-form>

      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button
          type="primary"
          :loading="editSaving"
          @click="saveEdit"
        >
          保存
        </el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="resetVisible"
      title="重置密码"
      width="480px"
      :close-on-click-modal="false"
    >
      <el-alert
        :title="`重置「${resettingUser?.username ?? ''}」的密码后，该账号的所有后台会话会立即失效。`"
        type="warning"
        :closable="false"
        show-icon
        class="reset-alert"
      />
      <el-form label-position="top" @submit.prevent="resetPassword">
        <el-form-item label="新密码" required>
          <el-input
            v-model="resetForm.password"
            data-test="reset-password"
            type="password"
            show-password
            autocomplete="new-password"
            maxlength="128"
            placeholder="至少 12 个字符"
          />
        </el-form-item>
        <el-form-item label="确认新密码" required>
          <el-input
            v-model="resetForm.confirm"
            type="password"
            show-password
            autocomplete="new-password"
            maxlength="128"
            placeholder="再次输入新密码"
          />
        </el-form-item>

        <el-alert
          v-if="resetError"
          :title="resetError"
          type="error"
          :closable="false"
          show-icon
        />
      </el-form>

      <template #footer>
        <el-button @click="resetVisible = false">取消</el-button>
        <el-button
          type="primary"
          :loading="resetSaving"
          @click="resetPassword"
        >
          确认重置
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
  margin: 0;
  color: #8393a7;
  font-size: 14px;
}

.table-card {
  border: 1px solid #e8edf4;
  border-radius: 12px;
}

.load-error {
  margin-bottom: 16px;
}

.username-cell {
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

.reset-alert {
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
