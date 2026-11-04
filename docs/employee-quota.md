# 员工额度与自助 Key 方案

本文描述从「按 Token 发额度」调整为「按员工发额度、Key 自助管理」的完整开发方案，作为实现与验收依据。

## 1. 目标

员工登录门户后看到自己的额度，可自助创建多个命名 Key；所有 Key 共享同一份额度；额度不足时提交提额申请，管理员批准后立即生效，员工继续使用原有 Key。

一句话流程：

> 员工登录 → 查看个人额度 → 自助建多个 Key（可命名）→ 所有 Key 共享扣减 → 额度不足 → 提交提额申请 → 批准后继续用原 Key。

## 2. 为什么这样做

主流厂商都把「凭据」和「额度」分层：

| 厂商 | 凭据 | 额度层 |
|---|---|---|
| DeepSeek | 账户下可建多个 API Key | 按账户余额扣减，402 |
| OpenAI | Project 作用域 Key | 按 Project / Organization 限额，429 |
| Anthropic | Workspace 作用域 Key | 按 Workspace 设置速率与花费上限 |

结论：Key 只是凭据，额度属于更高一层（账户 / 项目 / 工作区）。本系统取「员工」为额度主体：

- **共享实际消耗池**：`employee_quotas.quota_used` 是唯一权威计数，任何 Key 的请求都扣这一份。
- **不是「各 Key 额度相加」**：审批只提高员工额度，不新增 Key，也不迁移 Key。
- **Key 级只保留可选上限与 RPM**：防止单个 Key 失控，不作为员工总预算。

## 3. 数据模型

### 3.1 新表 `employee_quotas`

| 列 | 类型 | 说明 |
|---|---|---|
| `employee_id` | uuid PK, FK employees | 一人一行 |
| `quota_type` | text | `tokens` / `requests` |
| `quota_period` | text | `daily` / `monthly` / `total` |
| `quota_total` | bigint | 周期额度上限，`0` 表示禁止调用 |
| `quota_used` | bigint | 当前周期已用（预留 + 已结算） |
| `period_start` | timestamptz | 当前周期起点（Asia/Shanghai） |
| `updated_by` | uuid nullable | 最近调整人（管理员） |
| `updated_at` | timestamptz | 最近调整时间 |

`quota_used` 同时承担「用量累计」职责，不再单建 `employee_usage` 表：网关预留在同一事务内锁行、校验、累加，避免两表计数漂移。历史用量仍可在 `request_logs` 按员工聚合查询。

### 3.2 `tokens` 变更

- 新增 `name text NOT NULL DEFAULT ''`：Key 名称，员工自建时填写，便于区分用途。
- `quota_*` / `period_start` 保留，语义改为「单 Key 可选上限」：
  - `quota_total IS NULL`：该 Key 不限制，只受员工额度约束。
  - `quota_total` 有值：请求同时检查员工额度与单 Key 上限。
- 现有 Token 的额度字段不删除，迁移期兼容；新 UI 将其标为「单 Key 上限（可选）」。

### 3.3 `token_requests` 变更

- 新增 `kind text NOT NULL DEFAULT 'quota'`：`quota`（提额，默认）/ `token`（历史兼容）。
- 状态新增 `granted`：提额申请批准即终态，不需要领取。
- 提额申请不再写 `granted_quota_type/period/rpm/expires_at/token_id`，只记录 `granted_total`（批准后的员工额度总量）与审批说明。
- `claim` 仅对历史 `kind='token'` 且 `status='approved'` 的申请保留。

### 3.4 迁移 `0005_employee_quota.sql`

1. 建 `employee_quotas`。
2. `tokens` 加 `name`。
3. `token_requests` 加 `kind`，放宽状态约束以接受 `granted`。
4. 回填：为每个已启用员工建额度行，初始总量取
   `GREATEST(默认额度, 其所有有效 Token 的 quota_total 之和)`，周期取默认 `monthly`，`quota_used` 取这些 Token 当前 `quota_used` 的最大值（保守估计，避免迁移后瞬间超额）。
5. 历史申请 `kind='token'`，保留领取流程。

## 4. 网关配额校验

### 4.1 流程

```
认证 → Token RPM 限流 → 解析模型/参数 → 协议/DLP
     → 员工额度预留（锁 employee_quotas 行）
     → 单 Key 上限校验（若配置）
     → 上游调用 → 结算实际用量
```

### 4.2 规则

- 预留量：`requests` 类型为 1；`tokens` 类型为 `max_tokens`，缺省用 `DefaultReserveTokens`。
- 周期：`daily` / `monthly` 按 Asia/Shanghai 边界滚动，`total` 不重置。
- 超额：员工额度不足返回 `402 quota_exceeded`，响应体带 `scope: "employee"`；单 Key 上限不足同样 402，带 `scope: "token"`。
- 结算：`actual = quota_used - reserved + actual_tokens`，员工行与 Key 行同一事务更新；周期已滚动则跳过旧的结算，避免跨周期回写。
- 降额保护：管理员把 `quota_total` 调到低于 `quota_used` 时允许保存，但现有请求全部 402，直到新周期或再次提额。
- 并发：`SELECT ... FOR UPDATE` 锁员工行，Key 级校验只读该员工行；高并发下不会超发。
- PostgreSQL 故障：返回 503 `quota_unavailable`（失败关闭）。

### 4.3 接口调整

`gateway.Quota` 改为：

```go
type Quota interface {
    Reserve(ctx context.Context, employeeID uuid.UUID, token *store.Token, endpoint quota.Endpoint, maxTokens int64) (store.Reservation, error)
    Settle(ctx context.Context, r store.Reservation, actual int64) error
}
```

`store.Reservation` 增加 `EmployeeID`、`TokenReserved`，结算时同时回写员工与 Key。

## 5. 自助 Key

### 5.1 门户能力

| 操作 | 接口 | 说明 |
|---|---|---|
| 查看额度 | `GET /portal/api/me/quota` | 总量、已用、剩余、周期、重置时间、是否可提额 |
| Key 列表 | `GET /portal/api/me/tokens` | 掩码、名称、单 Key 上限、RPM、状态、最后使用 |
| 创建 Key | `POST /portal/api/me/tokens` | 名称必填（2–40 字），明文仅返回一次 |
| 轮换 | `POST /portal/api/me/tokens/:id/rotate` | 生成新明文并立即吊销旧 Key |
| 吊销 | `POST /portal/api/me/tokens/:id/revoke` | 立即失效，不可恢复 |

约束：

- 只能操作自己的 Key；他人 ID 返回 404。
- 每个员工有效 Key 数量上限由 `max_active_tokens` 控制（默认 5）。
- 创建/轮换/吊销写入 `employee_audit_logs`，不记录明文。
- 轮换响应 `Cache-Control: no-store`。
- 管理员可在设置中关闭自助创建 / 轮换 / 吊销。

### 5.2 管理端 Key 管理

保留现有 Token 管理页，新增：

- 名称列与筛选；
- 「单 Key 上限」列（空 = 不限制）；
- 员工额度快速查看入口；
- 管理员代建 Key 时同样受 `max_active_tokens` 约束（可配置是否豁免）。

## 6. 提额申请

### 6.1 员工侧

1. 员工在「额度申请」页看到当前额度与已用量。
2. 填写目标额度（必须大于当前总量且不超过 `max_request_total`）与理由（10–500 字）。
3. 同一员工同时只能有一条 `pending` 申请。
4. 批准后状态为 `granted`，额度立即生效，无需领取；员工继续使用原 Key。
5. 被拒绝显示管理员原因；员工可取消 pending 申请。

### 6.2 管理端审批

- 申请列表显示员工、部门、当前额度、目标额度、理由、时间。
- 批准时可调整最终批准额度（不得超过 `max_request_total`），填写说明。
- 批准事务：更新 `employee_quotas.quota_total`、写 `granted` 状态、写 `admin_audit_logs`，三者原子完成。
- 拒绝必须填写原因。
- 历史 `kind='token'` 申请仍按原「批准 → 领取」流程处理。

### 6.3 管理员直接调整额度

- 员工详情/团队管理提供「调整额度」：设置总量、周期、类型。
- 支持按部门批量设置默认额度（写入该部门所有员工，个人覆盖优先）。
- 所有调整写审计，记录调整前后值。

## 7. 门户与管理端页面

### 7.1 门户

- **概览**：增加额度卡片（已用 / 总量、进度条、重置时间、剩余可用）。
- **我的 Key**：额度卡片 + Key 表格 + 新建 / 轮换 / 吊销；空状态引导「创建第一个 Key」。
- **额度申请**：当前额度、目标额度、理由、历史申请与状态。
- **用量**：保持按请求明细，可按 Key 过滤。

### 7.2 管理端

- **团队管理**：员工列表增加「额度」列（已用/总量）与「调整额度」操作。
- **Token 管理**：名称、单 Key 上限、员工归属；创建对话框改为「Key 名称 + 可选上限 + RPM」。
- **额度审批**：原「Token 申请」页改为「额度审批」，展示当前/目标额度；历史 Token 申请单独标记。
- **系统设置 → 员工门户**：新增
  - `self_service_enabled`（默认 true）
  - `default_employee_quota`（type/period/total/rpm）
  - `max_active_tokens`（默认 5）
  - `allow_self_rotate`、`allow_self_revoke`（默认 true）
  - `auto_approve_quota`（默认 false；开启后提额申请直接 `granted`）
- **仪表盘**：额度压力改为按员工聚合（员工名、已用/总量、剩余、重置时间、活跃 Key 数）。

## 8. 兼容与迁移

- 现有 Token 继续可用；迁移后其额度不再是员工总额度，而是单 Key 上限。
- 迁移回填采用保守策略（取最大已用、总额取各 Key 之和与默认值的较大者），避免上线即超额。
- `claim_ttl_hours` 仅对历史 `kind='token'` 生效；新 UI 不再展示领取入口。
- 保留 `token_requests` 老数据与 `Claim` 接口，直至所有历史申请处理完毕。
- 回滚：新表与新列可保留，旧代码忽略即可；不删除任何历史列。

## 9. 安全与审计

- 明文 Key 只在创建/轮换响应中出现一次，落库仅 SHA-256 哈希；响应 `no-store`。
- 所有门户写操作要求 CSRF 双提交；员工 ID 只取会话。
- 提额、直接调额、自助 Key 操作均写审计；审计失败不阻断业务。
- 额度查询与预留失败关闭，不降级放行。
- 管理端调整额度需要 `operator` 及以上；查看审计需要 `auditor` 及以上。

## 10. 测试

- **Store**：额度行并发预留不超发；周期滚动重置；降额后 402；结算跨周期跳过；Key 上限独立生效；提额事务原子性。
- **网关**：员工额度不足 402 `scope=employee`；Key 上限不足 402 `scope=token`；数据库故障 503；流式与非流式结算一致。
- **门户 API**：自助创建/轮换/吊销、越权 404、数量上限、提额申请校验与批准生效、历史 claim 兼容。
- **管理 API**：审批权限、批准额度上限、直接调额审计、设置项校验。
- **前端**：门户 Key 页创建/轮换/吊销、额度卡片、提额申请；管理端审批与设置表单；类型检查与构建通过。

## 11. 实施顺序

1. 迁移 `0005` 与模型/Store（员工额度、Key 名称、申请 kind）。
2. 网关额度服务与结算改造。
3. 门户 API（额度、自助 Key、提额申请）。
4. 管理 API（额度调整、审批、设置、仪表盘）。
5. 前端类型与页面。
6. 测试、文档与回归。

## 12. 验收标准

- 员工可自助创建多个命名 Key，全部共享同一额度计数。
- 任一 Key 消耗后，门户与管理端看到的已用量一致。
- 额度耗尽时所有 Key 返回 402；提额批准后原 Key 立即恢复可用。
- 轮换后旧 Key 立即 401，新 Key 可用；吊销同理。
- 历史 Token 与历史申请仍可查询和领取。
- 管理端可查看员工额度、审批提额、按 Key 查看用量；所有变更可审计。
