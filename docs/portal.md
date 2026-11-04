# 员工自助门户

员工门户是管理后台之外的独立登录入口，挂载在同一站点的 `/portal` 路径下。员工用「工号 + 密码」登录后，可以查看个人额度与用量、自助创建/轮换/吊销多个 Key，并在额度不足时提交提额申请。额度按员工共享，多个 Key 只是凭据，不单独占用额度。

门户与管理后台共用前端构建产物，但会话、CSRF Cookie 与 API 客户端完全隔离：

| 项目 | 管理后台 | 员工门户 |
|---|---|---|
| 页面路径 | `/admin/*`（入口 `/admin/login`） | `/portal/*`（入口 `/portal/login`） |
| API 前缀 | `/admin/api` | `/portal/api` |
| 会话 Cookie | `admin_session`（HttpOnly，Path=`/admin/api`） | `employee_session`（HttpOnly，Path=`/portal`） |
| CSRF Cookie | `csrf_token`（Path=`/admin`） | `employee_csrf`（Path=`/portal`） |

门户会话不能满足管理后台请求，反之亦然。员工 ID 始终取自会话，接口不接受客户端传入的 `employee_id`。

## 1. 启用门户

门户默认关闭。迁移 `0003_employee_portal.sql` 执行后，需要超级管理员在 **系统设置 → 员工门户** 中打开开关。

可配置项（保存在 `settings` 表的 `portal` 键下，JSONB）：

| 字段 | 默认值 | 说明 |
|---|---|---|
| `enabled` | `false` | 关闭时所有 `/portal/api` 路由返回 404 |
| `default_quota.type` | `tokens` | 默认额度类型：`tokens` / `requests` |
| `default_quota.period` | `monthly` | 默认周期：`daily` / `monthly` / `total` |
| `default_quota.total` | `1000000` | 默认额度 |
| `default_quota.rpm` | `60` | 默认每分钟请求数 |
| `default_quota.expires_days` | `180` | Token 默认有效期（天） |
| `max_request_total` | `5000000` | 员工单次可申请、管理员单次可批准的额度上限 |
| `request_ttl_days` | `14` | 申请单在审批前有效天数 |
| `claim_ttl_hours` | `72` | 旧版 Token 申请的领取窗口（仅兼容历史申请） |
| `usage_max_days` | `90` | 员工可查询的最长历史区间 |
| `self_service_enabled` | `true` | 允许员工自助创建 Key |
| `max_active_tokens` | `5` | 每个员工最多同时有效的 Key 数（1–50） |
| `allow_self_rotate` | `true` | 允许员工自助轮换 Key |
| `allow_self_revoke` | `true` | 允许员工自助吊销 Key |
| `auto_approve_quota` | `false` | 提额申请是否自动批准；默认人工审批 |

策略修改后立即生效；门户进程内有 10 秒缓存，设置保存时会主动失效。

## 2. 开通员工账号

1. 管理员打开 **团队管理**，找到目标员工，点击「门户账号」。
2. 点击「开通门户账号」，系统生成 16 位一次性初始密码并只展示一次。
3. 将工号与初始密码通过可信渠道交给员工，不要粘贴到群聊或工单截图。
4. 员工首次登录会被强制跳转到修改密码页；改密成功后所有旧会话失效，需要重新登录。

「重置密码」会生成新的初始密码、清除失败计数与锁定状态，并撤销该员工全部门户会话。「停用账号」会删除门户凭据并撤销会话；员工档案本身不受影响，可随时重新开通。

登录保护：

- 同一 IP 每分钟最多 30 次登录尝试，同一工号每分钟最多 10 次。
- 连续 5 次密码错误后锁定 15 分钟；锁定期间即使密码正确也返回 429，管理员重置密码可立即解锁。
- 密码错误时，无论工号是否存在、账号是否已锁定，都返回相同的 401；锁定状态只会被持有正确密码的本人在 429 中感知，避免把登录响应当作账号枚举工具。
- 修改密码接口按员工维度限流（每分钟 10 次），并使用条件更新，避免并发登录或管理员重置被旧值覆盖。
- 限流器不可用时登录和改密失败关闭（返回 503），不会跳过校验。
- 员工档案被停用（`status != 1`）或门户关闭时，登录返回 404/401，且不泄露账号是否存在。

## 3. 个人额度与自助 Key

额度主体是员工，不是单个 Key。员工的全部 Key 共享同一个「已用额度」计数；每个 Key 只保存凭据，并可选保留一个单 Key 上限用于隔离单个应用的消耗。

### 3.1 自助 Key

1. 员工在 **我的 Key** 页点击「创建 Key」，填写 2–40 字的名称，可选单 Key 上限与 RPM。
2. 明文 Key 只在创建响应中出现一次，数据库只保存 SHA-256 哈希与前后缀；响应带 `Cache-Control: no-store`。
3. 员工可随时轮换 Key：系统在同一事务内创建同名新 Key 并停用旧 Key，新明文只展示一次；并发轮换同一 Key 时只有一个请求成功。
4. 员工可吊销自己的 Key；吊销后立即无法用于网关请求，且不能通过轮换“复活”，只能重新创建。
5. 有效 Key 数量达到 `max_active_tokens` 时，创建返回 409 `token_limit_reached`。

自助创建、轮换、吊销分别受 `self_service_enabled`、`allow_self_rotate`、`allow_self_revoke` 控制。关闭时接口返回 403 `self_service_disabled` / `self_rotate_disabled` / `self_revoke_disabled`。

### 3.2 提额申请

流程为「员工申请 → 管理员审批 → 额度立即生效」：

1. 员工在 **额度提额** 页填写理由（10–500 字）与目标总额度，提交后状态为 `pending`。
2. 目标额度必须大于当前额度且不超过 `max_request_total`；同一员工同时只能有一条进行中的申请，申请超过 `request_ttl_days` 后自动过期。
3. 操作员或超级管理员在管理后台 **Token 申请** 页批准或拒绝：
   - 批准时确认新的总额度与可选周期，额度不得超过 `max_request_total`。
   - 拒绝必须填写原因，员工可在门户中看到。
4. 批准后状态直接变为 `granted`，员工额度立即更新；员工继续使用原 Key，无需领取或更换。
5. 开启 `auto_approve_quota` 后，提交申请即自动批准并立即生效。

> 兼容：迁移 `0005_employee_quota.sql` 之前创建的 `kind='token'` 申请仍保留「审批 → 领取」流程，管理员批准后员工可在门户领取一次明文。新申请固定为 `kind='quota'`，不再创建 Key。

## 4. 员工可见数据

| 页面 | 接口 | 说明 |
|---|---|---|
| 概览 | `GET /portal/api/me/summary` | 请求数、Token、错误、P95 延迟与每日趋势 |
| 用量明细 | `GET /portal/api/me/usage` | 仅元数据（模型、状态码、Token、延迟、DLP 动作），不返回提示词或响应原文 |
| 个人额度 | `GET /portal/api/me/quota` | 总额度、已用、剩余、重置时间、有效 Key 数与自助开关 |
| 我的 Key | `GET/POST /portal/api/me/tokens` | 列表与自助创建；响应含一次明文 |
| Key 轮换/吊销 | `POST /portal/api/me/tokens/:id/rotate` / `revoke` | 轮换返回新明文；只能操作自己的 Key |
| 额度提额 | `GET/POST /portal/api/me/token-requests` | 列表、提交、取消；历史 `kind='token'` 申请可领取 |
| 个人信息 | `GET /portal/api/auth/me` | 工号、姓名、邮箱、部门 |

所有查询都以会话中的员工 ID 过滤。访问其他员工的申请 ID 返回 404。未修改初始密码时，`/me/*` 数据接口返回 403 `password_change_required`；`/auth/me`、退出登录与改密仍可用。

## 5. 安全要点

- 密码使用 Argon2id 哈希，初始密码只返回一次，服务端与日志均不保存明文。
- Key 明文只在创建或轮换响应中生成一次，落库的是 SHA-256 哈希；审计与日志不得记录明文。
- 门户 Cookie 的 `Path=/portal`，并随部署的 HTTPS 配置启用 `Secure`；会话 Cookie 为 HttpOnly。
- 所有非安全方法（POST/PUT/DELETE）要求 `X-CSRF-Token` 与 `employee_csrf` 双提交匹配。
- 登录、改密、Key 创建/轮换/吊销、提额申请与取消等员工操作写入 `employee_audit_logs`；审批与账号操作写入 `admin_audit_logs`。审计写入失败不会阻断业务请求。
- 门户 API 依赖 PostgreSQL 完成认证与限流；数据库不可用时失败关闭。

## 6. 排障

| 现象 | 可能原因 | 处理 |
|---|---|---|
| `/portal` 返回 404 | 门户未启用 | 在系统设置中打开「员工门户」 |
| 登录返回 404 | 门户关闭、员工不存在或已停用 | 检查门户开关与员工状态 |
| 登录返回 429 | IP/工号维度限流，或密码正确但账号已锁定 15 分钟 | 等待一分钟；如确认密码正确，等待锁定结束或请管理员重置密码 |
| 登录返回 503 | 数据库或限流依赖不可用 | 检查 gateway 日志与 PostgreSQL |
| 提交申请返回 409 | 已有进行中的申请 | 等待审批或先取消原申请 |
| 创建 Key 返回 409 `token_limit_reached` | 有效 Key 数已达上限 | 吊销不用的 Key，或请管理员调高 `max_active_tokens` |
| 创建/轮换/吊销返回 403 | 对应自助开关已关闭 | 请管理员在系统设置中开启 |
| 网关返回 402 `scope=employee` | 个人额度已用尽 | 在门户提交提额申请 |
| 网关返回 402 `scope=token` | 单 Key 上限已用尽 | 换用其他 Key 或降低该 Key 的消耗 |
| 轮换返回 409 `token_revoked` | 该 Key 已撤销 | 重新创建 Key；已撤销的 Key 不会复活 |
| 旧版申请领取返回 409 | 超过领取窗口或已被领取 | 联系管理员重新审批 |
| 数据接口返回 403 | 尚未修改初始密码 | 完成强制改密 |

更多部署信息见 [生产部署](operations/deploy.md)，员工接入 SDK 的方法见 [员工接入指南](operations/employee-onboarding.md)。
