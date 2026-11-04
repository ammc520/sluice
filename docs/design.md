# Sluice — 企业 API 安全网关与 DLP 监测系统设计文档

- 日期：2026-11-02
- 状态：已确认
- 部署形态：腾讯云单机、Docker Compose、真实生产使用
- 初始规模：10 名员工，约 5,000 次请求/天，后续增长

## 1. 背景与目标

公司目前把 OpenAI、Claude、DeepSeek 等上游 API Key 直接分发给员工。没有统一入口、使用台账和泄露拦截能力。员工可能把客户信息、项目代号、内部代码等敏感内容直接粘贴进大模型，公司既看不见，也无法在发送前阻止。

本项目要建立一个所有员工调用大模型的统一入口，并配套管理后台：

1. **统一分发**：员工只持有公司签发的 `sk-company-*` Key，永远拿不到真实上游 Key。
2. **可控使用**：按员工、部门设置额度、有效期、白名单 IP 和速率限制，额度耗尽返回 402。
3. **泄露防护**：转发前扫描请求内容，按规则执行 Block / Mask / Audit。
4. **可追溯**：记录每次调用的员工、Token、IP、模型、状态码、耗时、Token 用量和 DLP 命中情况。
5. **可运维**：单机 Docker Compose 部署，数据库备份、健康检查、邮件告警、降级策略齐全。

### 成功标准

- 员工只改 `base_url` 和 API Key，就能继续使用 OpenAI 兼容 SDK / Claude SDK。
- SSE 流式响应逐块透传，不缓存、不攒包；网关自身附加延迟 p95 ≤ 10ms（不含上游耗时）。
- DLP 能命中内置敏感规则和后台维护的项目关键词。
- 审计日志异步落库，主请求路径不因数据库写入阻塞。
- PostgreSQL 或 Redis 故障时，网关行为明确、可恢复，不出现静默放行或整站瘫痪。
- 管理后台能完成员工、Token、上游、DLP 规则、审计查询和告警配置。

### 非目标（第一版不做）

- ClickHouse。当前数据量用 PostgreSQL 足够；审计存储抽象成接口，后续可替换。
- Kubernetes、多租户、跨地域高可用。
- 企业微信 / 飞书 / SSO 登录。管理后台使用独立管理员账号。
- 两人审批流程。敏感原文仅超级管理员可查看，且每次查看写入管理员审计日志。
- 对上游响应内容做 DLP 改写。第一版只扫描请求；响应内容加密留存。
- OpenAI ↔ Anthropic 协议转换。OpenAI 格式客户端不能直接调用 Claude；两套协议分开。
- 图片、文件、音频输入。第一版只支持纯文本对话与嵌入。
- 计费、发票、成本分摊。
- 员工自助门户。员工只需要调用 API。

### 反目标（即使功能齐全也算失败）

- 为了“可用性”在 Token 校验失败时默认放行。
- 把 Prompt / Response 明文写入数据库、日志或邮件。
- 在请求主路径同步写 PostgreSQL / ClickHouse。
- Redis 故障导致所有 API 调用不可用。
- 为了追求功能齐全，把一个 10 人团队的系统做成无法运维的平台。

## 2. 关键决策

| 决策 | 选择 | 理由 |
|---|---|---|
| 网关语言/框架 | Go + Gin + `net/http` | 高并发、SSE 透传成熟、部署简单 |
| 服务形态 | 单个 Go 二进制，同时提供 `/v1/*` 和 `/admin/api/*` | 10 人规模无需拆分微服务；后续可按路由拆 |
| 管理后台 | Vue 3 + TypeScript + Element Plus + ECharts | 生态成熟，适合内部管理台 |
| 主数据库 | PostgreSQL 16 | 事务、JSONB、加密字段、聚合查询都够用 |
| 审计存储 | 第一版 PostgreSQL，抽象 `AuditStore` | 5,000 条/天约 180 万条/年，Postgres 可承受；避免维护 ClickHouse |
| 缓存 / 限流 | Redis 7，可降级 | Token 缓存、分布式限流；Redis 不做额度真相源 |
| 额度真相源 | PostgreSQL 原子更新 | 避免 Redis 与数据库双写不一致 |
| 敏感字段加密 | AES-256-GCM，信封格式带 key id | 满足落盘加密；支持后续密钥轮换 |
| 上游 Key 存储 | 数据库加密字段，只写不读 | 后台只显示末四位，永不回传明文 |
| 管理员认证 | 独立账号 + 服务端 Session + HttpOnly Cookie + CSRF | 不接 SSO；比 JWT 更容易吊销 |
| 告警 | 后台记录 + SMTP 邮件，异步发送 | 当前需求；不引入第三方 IM 依赖 |
| DLP 扫描范围 | 仅请求体（含 OpenAI messages / Anthropic messages） | 防止公司信息外发；响应扫描/改写留到后续 |
| 部署 | 腾讯云单机 + Docker Compose + Caddy | 自动 HTTPS、配置少、故障面小 |
| ClickHouse | 第一版不部署 | 当前规模过度设计；保留迁移路径 |

## 3. 系统架构

```text
                        Internet / 公司网络
                                │
                                ▼
                    ┌─────────────────────┐
                    │  Caddy (80/443)     │
                    │  TLS / 静态后台     │
                    └─────────┬───────────┘
                              │
              ┌───────────────┴────────────────┐
              │                                │
       /v1/*  │                                │  /admin/api/*
              ▼                                ▼
    ┌──────────────────────────────────────────────────┐
    │              Go Gateway (Gin, :8080)             │
    │                                                  │
    │  Auth → RateLimit → Quota → DLP → Upstream Proxy │
    │    │        │          │       │          │      │
    │    │        │          │       │          │      │
    │    │        │          │       └─ audit chan ────┼──► Audit Worker ──► PostgreSQL
    │    │        │          │                        │         │
    │    │        │          └─ PostgreSQL (source)   │         └─► SMTP / 邮件
    │    │        └─ Redis (cache / rate limit)       │
    │    └─ PostgreSQL (tokens / users / rules)       │
    └──────────────────────────────────────────────────┘
                              │
                              ▼
                 OpenAI / Anthropic / DeepSeek
```

### 组件职责

- **Caddy**：TLS 终止、静态后台托管、把 `/v1/*` 与 `/admin/api/*` 反代到 Go 服务，设置禁止缓冲的响应头。
- **Gateway**：Token 校验、配额预留/结算、DLP 扫描与改写、上游路由、SSE 透传、用量解析、审计事件投递。
- **Admin API**：管理员登录、员工/Token/上游/规则/审计/设置管理。
- **Audit Worker**：批量加密落库、数据库失败重试、磁盘 spool 兜底、邮件告警。
- **PostgreSQL**：业务元数据、额度、审计日志、管理员会话、设置。
- **Redis**：Token 缓存、RPM 令牌桶、Dashboard 短缓存；故障时降级。
- **Caddy 静态站点**：Vue 管理后台。

## 4. 请求处理流程

### 4.1 代理请求

1. 接受两种公司凭证：`Authorization: Bearer sk-company-*`（OpenAI 兼容客户端）或 `x-api-key: sk-company-*`（Anthropic SDK 默认）。两者都只用于网关校验，转发前必须移除。取 SHA-256 摘要查 Token（Redis 缓存优先，未命中查 PostgreSQL）。
2. 校验 Token 状态、过期时间、白名单 IP、员工状态。失败返回 401；Token 已禁用同样返回 401，但审计记录原因。
3. 按 Token 的 RPM 做 Redis 令牌桶限流。超限返回 429，带 `Retry-After`。
4. 读取请求体，限制 2 MiB；JSON 解析失败时按原始文本处理，不直接拒绝（某些上游接口可能接受非 JSON）。
5. 解析 `model`，按路由表选择上游；找不到路由返回 400。
6. DLP 扫描：
   - 命中 Block 规则：返回 403，记录审计和告警，不转发。
   - 命中 Mask 规则：改写请求体，继续。
   - 仅命中 Audit 规则：继续，标记命中。
7. 配额预留：
   - 次数额度：原子占用 1 次。
   - Token 额度：按请求中的 `max_tokens` / `max_completion_tokens` 预留；未提供时使用默认预留值（4096）。预留失败返回 402。
8. 替换上游认证头，转发请求；移除客户端传入的 `Authorization`、`x-api-key`、`Host` 等敏感/冲突头。
9. 非流式响应：读取上游响应，透传状态码和内容；解析 usage。
10. 流式响应：逐块写回并在每块后 flush；同时旁路解析 SSE 事件提取 usage 和内容，最多保留 256 KiB 用于审计。
11. 用实际 usage 结算配额：退还预留与实际使用的差额；若上游未返回 usage，则按预留值计费并标记 `usage_estimated=true`。
12. 投递审计事件到有界 channel，立即返回，不等待落库。
13. 客户端断连：取消上游请求；已产生用量的按已知 usage 结算，未知则按预留结算并记录 `client_disconnected=true`。

### 4.2 状态码约定

| 场景 | 状态码 | 响应 |
|---|---|---|
| Token 缺失/无效/禁用/过期/IP 不允许 | 401 | 统一错误体，不暴露具体校验细节 |
| 额度耗尽 | 402 | `quota_exceeded` |
| DLP Block | 403 | `dlp_blocked`，包含 request_id 和规则名，不回显敏感内容 |
| 限流 | 429 | `rate_limited` + `Retry-After` |
| 上游连接失败 | 502 | `upstream_unavailable` |
| 上游超时 | 504 | `upstream_timeout` |
| 请求体过大 | 413 | `payload_too_large` |
| 其他网关错误 | 500 | 不返回堆栈或内部地址 |

统一错误体：

```json
{
  "error": {
    "code": "dlp_blocked",
    "message": "Request blocked by data loss prevention policy",
    "request_id": "01J..."
  }
}
```

上游返回的 4xx/5xx 原样透传状态码和响应体（但记录审计），避免破坏客户端的错误处理。

## 5. 上游路由

### 5.1 Provider

`upstream_providers` 表保存：

- `name`：如 `openai-prod`、`anthropic-prod`、`deepseek`。
- `protocol`：`openai_compatible` 或 `anthropic`。
- `base_url`：如 `https://api.openai.com`；不得以 `/` 结尾。
- `api_key_enc`：AES-256-GCM 加密后的真实 Key。
- `enabled`、`timeout_seconds`。

### 5.2 路由规则

- 路径优先判断协议：
  - `/v1/messages`、`/v1/messages/*` → `anthropic` 协议。
  - `/v1/chat/completions`、`/v1/embeddings`、`/v1/responses`、`/v1/models`、`/v1/completions` → `openai_compatible` 协议。
  - 其他 `/v1/*` 按请求体是否存在 `messages` 且无 `model` 等特征判断；无法判断时返回 400。
- 模型路由表 `model_routes`：
  - `protocol` 必须与请求路径推断出的协议一致，防止把 OpenAI 请求路由到 Anthropic Provider。
  - `pattern` 支持精确匹配、前缀匹配（`claude-*`）和通配符。
  - `provider_id` 指定上游。
  - `upstream_model` 可选：把员工请求的模型名替换为上游真实模型名。
  - 优先级数字越小越先匹配。
- 未命中模型路由时，使用该协议下 `is_default=true` 的 Provider；没有默认 Provider 返回 400。
- 支持后续增加自建模型：只要提供 OpenAI 兼容接口，配置 `openai_compatible` 即可。

### 5.3 请求与响应头

- 保留：`Content-Type`、`Accept`、`anthropic-version`、`anthropic-beta`、`OpenAI-Beta`、`Idempotency-Key`（按需）、客户端自定义业务头（白名单）。
- 丢弃：`Authorization`、`x-api-key`、`Host`、`Content-Length`、`Connection`、`Transfer-Encoding`、`Proxy-*`。
- 注入：对应上游的认证头；`X-Request-Id`；`User-Agent: CompanyAPIGateway/1.0`（可配置）。
- 响应：保留上游 `Content-Type`；对 SSE 设置 `Cache-Control: no-cache`、`X-Accel-Buffering: no`，Caddy 对该路径禁用缓冲。
- 超时：连接超时 10s，响应头超时 120s；流式读取不设总超时，但设置空闲超时 300s；由客户端取消驱动上下文取消。
- 不在第一版做自动重试。LLM POST 请求重试可能重复计费。

### 5.4 SSE 透传

- 使用 `http.Client` 直接读取上游响应体，不使用会整体缓冲的中间件。
- 每读到一块就写客户端并调用 `Flush()`；不按行缓存整个响应。
- 旁路解析器按行读取 `data:` 与 Anthropic `event:` 结构，提取 usage：
  - OpenAI：最终 chunk 的 `usage`；若上游不返回，标记估算。
  - Anthropic：`message_start` 的 input tokens 与 `message_delta` 的 output tokens。
  - DeepSeek：兼容 OpenAI 的 usage 字段。
- 捕获内容上限 256 KiB；超过后继续透传但停止记录，审计标记 `response_truncated=true`。
- 客户端断开时关闭上游响应体，终止 goroutine。

### 5.5 第一版支持的端点

第一版只支持文本类接口：

- OpenAI 兼容：`POST /v1/chat/completions`、`POST /v1/completions`、`POST /v1/embeddings`、`GET /v1/models`
- Anthropic：`POST /v1/messages`、`POST /v1/messages/count_tokens`
- 其他 `/v1/*` 返回 501 `unsupported_endpoint`：包括 Responses API、files、audio、images、batches、Realtime/WebSocket，以及图片/文件/音频 content block。原因是 DLP 无法可靠检查二进制内容，且不同 SSE 事件流会显著扩大第一版范围。
- OpenAI 与 Anthropic 是两套独立协议，不做格式转换。模型路由必须与路径协议一致；后台保存路由时校验，运行时不匹配返回 400。

## 6. DLP 规则引擎

### 6.1 规则模型

每条规则包含：

- `name`、`description`、`category`
- `match_type`：`regex` / `keyword`
- `pattern`：正则表达式或关键词；关键词按大小写不敏感匹配
- `action`：`block` / `mask` / `audit`
- `replacement`：Mask 时替换文本，默认 `***`
- `priority`：数值越小越先处理
- `enabled`、`builtin`、`alert_enabled`

### 6.2 内置规则（第一版）

| 类别 | 示例 | 默认动作 |
|---|---|---|
| 中国大陆身份证号 | 18 位，带基本校验位校验 | Mask |
| 手机号 | 中国大陆 11 位手机号 | Mask |
| 银行卡号 | 16–19 位数字 + Luhn 校验 | Mask |
| 内网 IP | 10/8、172.16/12、192.168/16、127/8 | Audit |
| 云厂商 AK/SK | AWS `AKIA...`、阿里云/腾讯云常见 Key 形态 | Block |
| 通用密钥 | `sk-...`、`Bearer ...`、私钥头 `-----BEGIN ... PRIVATE KEY-----` | Block |
| 邮箱地址 | 公司邮箱与常见邮箱 | Audit |

内置规则可禁用、可调整动作，但不可删除；升级时只补齐缺失项，不覆盖管理员改动。

### 6.3 自定义规则

管理员在后台维护，重点支持：

- 项目代号、产品名、内部系统名（关键词）
- 公司域名、内部主机名（正则）
- 客户名单、合同编号格式（正则）
- 源代码特征（如公司私有包前缀、内部 SDK 名称）
- 其他任意正则

提供“规则测试”接口：输入一段样例文本，返回命中规则、起止位置和最终动作。后台必须支持导入关键词清单（每行一个）。

### 6.4 扫描与改写

- 扫描对象：请求 JSON 中所有字符串值，覆盖 OpenAI `messages[].content`（字符串或 content block 的 `text`）、`tools` 描述与参数、Anthropic `system`、`messages`、`tool_use` 等位置。
- 为避免破坏协议结构，以下字段不扫描/不改写：`model`、`role`、`type`、`id`、`stream`、`max_tokens`、`temperature` 等控制字段。
- 跳过 `image_url`、`source.data`、`file_data`、`input_audio` 等二进制/base64 字段。第一版对包含图片、文件、音频 content block 的请求返回 415，避免 DLP 盲区、误改写和超大请求体。
- 非 JSON 请求体按原始文本扫描和替换。
- 同一文本命中多条规则时：任一 `block` 则整体阻断；否则执行所有 `mask`；`audit` 只记录。
- Mask 采用区间合并后从后往前替换，避免位移错乱。
- 正则使用 Go RE2，禁止灾难性回溯；单条规则编译失败时禁用该规则并告警，不影响其他规则。
- 扫描前限制文本总大小（请求体 2 MiB）；单次扫描目标 p95 ≤ 5ms（典型 10–50 KiB 请求）。
- 第一版不扫描上游响应。响应内容加密保存，供事后调查。

### 6.5 动作语义

- **Block**：返回 403，不转发；异步写审计 + 邮件告警。
- **Mask**：把命中片段替换后转发；审计记录规则名、动作、命中次数，保存原始请求（加密）以便调查；上游看到的是脱敏内容。
- **Audit**：原样转发；审计标记命中；按规则配置发送邮件告警。

### 6.6 误报控制

- 内置规则可单独禁用。
- 提供规则优先级和动作覆盖：例如手机号默认 Mask，管理员可改为 Audit。
- 第一版不做按员工的白名单豁免；如果误报严重，应调整规则或暂时禁用。
- 告警邮件按 `规则 + 员工` 5 分钟去重，避免刷屏。

## 7. 配额、限流与 Token 生命周期

### 7.1 Token 格式与存储

- 格式：`sk-company-` + 32 字节随机数的 base64url（无填充）。
- 数据库只存 SHA-256 十六进制摘要，不存明文；另存前 12 位作为后台展示前缀。
- 创建成功后仅展示一次完整 Key，后台之后只能看到前缀和末四位。
- 员工可持有多个具名 Key；所有 Key 共享员工额度，Key 仅作为凭据。
- 支持禁用、过期、轮换（新建同名 Key + 禁用旧 Key）与自助吊销。

### 7.2 额度

额度主体是员工，不是单个 Token。员工的全部 Key 共享 `employee_quotas` 中的同一个 `quota_used`；Key 上只保留可选的单 Key 上限（`tokens.quota_total`，NULL 表示不限额），用于隔离单个应用的消耗。

- 员工额度：`employee_quotas.quota_type` 为 `requests`（按次数）或 `tokens`（按 Token 用量）。
- `quota_period`：`daily`、`monthly`、`total`。
- `quota_total` 为当前允许额度；`quota_used` 为当前周期已用。
- 周期边界按 `Asia/Shanghai` 计算；访问时惰性重置：若当前时间已跨周期，先把 `quota_used` 归零并更新 `period_start`。
- 429 与 402 区分：RPM 超限是 429；额度耗尽返回 402 Payment Required，并通过 `scope`（`employee` / `token`）区分个人池与单 Key 上限。
- 预留与结算：
  - 次数额度：请求前原子 +1，失败不退还（因为已消耗一次尝试）；上游连接失败可退还，按产品策略配置，默认退还。
  - Token 额度：请求前预留 `max_tokens`，响应后按实际 usage 结算，退还差额；usage 缺失按预留计费。
  - 单 Key 上限与员工额度在同一事务内预留；Key 上限不足时整体回滚，不会泄漏员工额度。
- 并发安全：在 PostgreSQL 事务中先对 `employee_quotas` 行做原子 upsert（锁住员工池），再更新 Key 行；重置周期、检查并更新额度都在单条 SQL 内完成。单机 10 人规模足够；后续可改为 Redis Lua 预检 + PostgreSQL 异步核对。
- 结算使用相对算术（`quota_used - reserved + actual`），并按 `period_start` 判断是否跨周期；跨周期结算只放弃旧周期的写入，不影响新周期。
- 计量端点分级：
  - 生成类（`chat/completions`、`messages`、`completions`）：按 `max_tokens` 预留，响应后结算。
  - Embeddings：请求前只检查剩余额度 > 0，响应后按 usage 结算；允许最后一次请求小幅超额。
  - 元数据类（`models`、`messages/count_tokens`）：不消耗 Token 额度，但仍计入 RPM 和审计。
  - 第一版不支持 Responses API；后续接入时必须单独解析其 SSE usage，不能复用 chat completions 的解析器。

### 7.3 速率限制

- 每个 Token 独立 RPM 令牌桶，默认 60 RPM，可配置。
- Redis Lua 原子扣减；键带 2 分钟 TTL。
- Redis 不可用时降级为本机内存令牌桶。单实例部署下精度足够；多实例部署前必须恢复 Redis 或改用其他共享存储。
- 管理员登录也做限流：每 IP 每分钟 5 次，连续失败 15 分钟后短暂锁定。

### 7.4 白名单 IP

- 支持 CIDR 列表，如 `203.0.113.0/24`、`198.51.100.7/32`。
- 为空表示不限制。
- 只信任来自 Caddy 的 `X-Forwarded-For` 最右侧可信地址；Go 服务监听内网端口，不直接暴露公网。

## 8. 审计日志与告警

### 8.1 审计内容

每次请求（包括被拒绝的请求）产生一条审计事件：

- 时间、request_id、token_id、员工、部门、客户端 IP、方法、路径、模型、上游 Provider
- 状态码、网关错误码、总耗时、上游耗时、首字节耗时
- 输入/输出/总 Token 数、是否估算
- DLP 命中规则列表、最终动作（none/block/mask/audit）
- 请求 Prompt 原文密文、响应原文密文、是否截断
- 是否客户端断连、上游请求 ID（如有）

不记录：完整上游 Key、公司 Token 明文、管理员密码、Cookie。

### 8.2 异步写入

- 主路径只向有界 channel（容量 4096）投递 `AuditEvent`，不等数据库。
- Audit Worker 批量写入：最多 200 条或每 1 秒 flush 一次。
- channel 满时：写入本地 spool 文件（加密、0600、按大小轮转），绝不阻塞请求；同时打点告警。
- 数据库写失败：指数退避重试 3 次；仍失败则写 spool。后台每 30 秒尝试回放 spool。
- 服务优雅关闭：停止接收新请求，关闭 channel，Worker 排空后退出；超时 10 秒后强制退出并保留 spool。
- 审计表只允许应用角色 INSERT/SELECT，不允许 UPDATE/DELETE；保留策略由独立维护任务执行。

### 8.3 加密

- Prompt / Response 使用 AES-256-GCM 加密后存 `bytea`。
- 信封格式：`version(1) | key_id(1) | nonce(12) | ciphertext+tag`。
- AAD 使用 `request_id + field_name`，防止密文字段互换。
- 主密钥来自环境变量 `MASTER_KEY`（base64 编码的 32 字节），不得写入数据库或代码库。
- 支持 `MASTER_KEY_PREVIOUS` 用于轮换期解密；新写入只用当前 key。
- 明文只在内存中存在；查看原文接口返回后设置 `Cache-Control: no-store`。

### 8.4 管理员查看原文

- 仅 `super_admin` 可调用 `POST /admin/api/audit/:id/reveal`。
- 请求必须带 `reason`（至少 5 个字符）。
- 每次查看写入 `admin_audit_logs`：操作人、时间、目标 request_id、字段、原因、IP。
- 前端弹出确认框，明确“此次查看会被记录”。
- 不做两人审批；这是本项目明确的范围取舍。

### 8.5 Dashboard

- 今日/近 7 天/近 30 天调用量、Token 消耗、活跃员工、DLP 命中数、错误率。
- 调用趋势折线图、Token 消耗排行、DLP 规则命中排行、最近告警。
- 查询直接聚合 PostgreSQL；为 `event_time`、`user_id`、`token_id`、`dlp_action` 建索引。
- Dashboard 查询可加 15 秒缓存；缓存失效不影响正确性。

### 8.6 邮件告警

- SMTP 配置放在 `settings` 表，密码加密保存；仅超级管理员可编辑。
- 告警类型：DLP Block、DLP Audit 命中（按规则开关）、审计队列溢出、数据库写入失败、额度 80%/100%（可选）。
- 异步发送，3 次指数退避重试；失败写系统日志，不阻塞请求。
- 邮件正文只包含员工、规则名、时间、request_id、动作，不包含 Prompt/Response 原文。
- 提供“发送测试邮件”按钮。

## 9. 数据模型

使用 PostgreSQL。所有时间字段为 `timestamptz`；主键使用 UUID 或 bigserial；迁移使用 goose 嵌入二进制。

```sql
CREATE TABLE departments (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE employees (
    id            UUID PRIMARY KEY,
    employee_no   VARCHAR(64) NOT NULL UNIQUE,
    name          VARCHAR(128) NOT NULL,
    email         VARCHAR(255),
    department_id BIGINT REFERENCES departments(id),
    status        SMALLINT NOT NULL DEFAULT 1,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE tokens (
    id             UUID PRIMARY KEY,
    employee_id    UUID NOT NULL REFERENCES employees(id),
    token_hash     CHAR(64) NOT NULL UNIQUE,
    token_prefix   VARCHAR(16) NOT NULL,
    token_last4    VARCHAR(4) NOT NULL,
    quota_type     TEXT NOT NULL DEFAULT 'tokens'
                   CHECK (quota_type IN ('requests','tokens')),
    quota_period   TEXT NOT NULL DEFAULT 'monthly'
                   CHECK (quota_period IN ('daily','monthly','total')),
    quota_total    BIGINT,                 -- NULL = 不限额
    quota_used     BIGINT NOT NULL DEFAULT 0,
    period_start   TIMESTAMPTZ,
    rate_limit_rpm INTEGER NOT NULL DEFAULT 60,
    ip_allowlist   JSONB NOT NULL DEFAULT '[]'::jsonb,
    status         SMALLINT NOT NULL DEFAULT 1,
    expires_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at   TIMESTAMPTZ
);

CREATE TABLE upstream_providers (
    id              UUID PRIMARY KEY,
    name            VARCHAR(128) NOT NULL UNIQUE,
    protocol        TEXT NOT NULL
                    CHECK (protocol IN ('openai_compatible','anthropic')),
    base_url        TEXT NOT NULL,
    api_key_enc     BYTEA NOT NULL,
    is_default      BOOLEAN NOT NULL DEFAULT false,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    timeout_seconds INTEGER NOT NULL DEFAULT 300,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE model_routes (
    id             UUID PRIMARY KEY,
    provider_id    UUID NOT NULL REFERENCES upstream_providers(id),
    protocol       TEXT NOT NULL
                   CHECK (protocol IN ('openai_compatible','anthropic')),
    pattern        TEXT NOT NULL,
    match_type     TEXT NOT NULL DEFAULT 'prefix'
                   CHECK (match_type IN ('exact','prefix','glob')),
    upstream_model TEXT,
    priority       INTEGER NOT NULL DEFAULT 100,
    enabled        BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE dlp_rules (
    id            UUID PRIMARY KEY,
    name          VARCHAR(128) NOT NULL UNIQUE,
    description   TEXT NOT NULL DEFAULT '',
    category      VARCHAR(64) NOT NULL,
    match_type    TEXT NOT NULL CHECK (match_type IN ('regex','keyword')),
    pattern       TEXT NOT NULL,
    action        TEXT NOT NULL CHECK (action IN ('block','mask','audit')),
    replacement   TEXT NOT NULL DEFAULT '***',
    priority      INTEGER NOT NULL DEFAULT 100,
    enabled       BOOLEAN NOT NULL DEFAULT true,
    builtin       BOOLEAN NOT NULL DEFAULT false,
    alert_enabled BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
    id                BIGSERIAL PRIMARY KEY,
    event_time        TIMESTAMPTZ NOT NULL,
    request_id        VARCHAR(64) NOT NULL UNIQUE,
    token_id          UUID,
    employee_id       UUID,
    department_id     BIGINT,
    client_ip         INET,
    method            VARCHAR(8) NOT NULL,
    path              TEXT NOT NULL,
    model             VARCHAR(128),
    provider_id       UUID,
    status_code       INTEGER NOT NULL,
    error_code        VARCHAR(64),
    latency_ms        INTEGER NOT NULL,
    upstream_latency_ms INTEGER,
    first_byte_ms     INTEGER,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens      INTEGER NOT NULL DEFAULT 0,
    usage_estimated   BOOLEAN NOT NULL DEFAULT false,
    prompt_enc        BYTEA,
    response_enc      BYTEA,
    prompt_truncated  BOOLEAN NOT NULL DEFAULT false,
    response_truncated BOOLEAN NOT NULL DEFAULT false,
    dlp_action        TEXT NOT NULL DEFAULT 'none'
                      CHECK (dlp_action IN ('none','block','mask','audit')),
    dlp_hits          JSONB NOT NULL DEFAULT '[]'::jsonb,
    client_disconnected BOOLEAN NOT NULL DEFAULT false,
    upstream_request_id TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_event_time ON audit_logs (event_time DESC);
CREATE INDEX idx_audit_employee_time ON audit_logs (employee_id, event_time DESC);
CREATE INDEX idx_audit_token_time ON audit_logs (token_id, event_time DESC);
CREATE INDEX idx_audit_dlp_time ON audit_logs (dlp_action, event_time DESC);
CREATE INDEX idx_audit_status_time ON audit_logs (status_code, event_time DESC);

CREATE TABLE admin_users (
    id            UUID PRIMARY KEY,
    username      VARCHAR(64) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL
                  CHECK (role IN ('super_admin','operator','auditor')),
    status        SMALLINT NOT NULL DEFAULT 1,
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE admin_sessions (
    id          UUID PRIMARY KEY,
    admin_id    UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    token_hash  CHAR(64) NOT NULL UNIQUE,
    csrf_hash   CHAR(64) NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE admin_audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    admin_id    UUID NOT NULL,
    action      VARCHAR(64) NOT NULL,
    target_type VARCHAR(64) NOT NULL,
    target_id   TEXT,
    detail      JSONB NOT NULL DEFAULT '{}'::jsonb,
    client_ip   INET,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE settings (
    key        VARCHAR(128) PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 9.1 索引与保留

- 审计日志默认保留 180 天，后台可配置；每日 03:00 清理超期数据。
- 按 `event_time` 建索引；暂不分区。若单表超过 2,000 万行或查询明显变慢，再迁移 ClickHouse。
- `audit_logs` 只有 INSERT/SELECT 权限；清理任务使用独立数据库角色。

### 9.2 用量与一致性

- Token 用量以 PostgreSQL 为真相源，Redis 只缓存 Token 元数据。
- Dashboard 聚合允许最终一致；审计异步写入有秒级延迟。
- 管理后台修改 Token / 规则 / Provider 后，立即删除对应 Redis 缓存；其他实例最多 30 秒后自然过期（当前单实例）。

## 10. 管理后台 API

### URL 命名空间

单域名部署，按用途划分三个顶层前缀，避免管理端、员工端与网关互相占用路径：

| 前缀 | 用途 | 说明 |
|---|---|---|
| `/v1/*` | 模型网关 | 客户端持员工 Key 调用，OpenAI / Anthropic 兼容 |
| `/admin/*`、`/admin/api/*` | 管理后台 | 页面入口 `/admin/login`；根路径下的旧页面（`/dashboard`、`/tokens` 等）已移除 |
| `/portal/*`、`/portal/api/*` | 员工门户 | 页面入口 `/portal/login`；站点根 `/` 跳转到门户 |

管理端 Cookie 也按此收敛：`admin_session` 仅 Path=`/admin/api`，`csrf_token` Path=`/admin`。升级前写入的根路径（Path=`/`）Cookie 会在下次登录/登出时清除，避免新旧会话并存。

API 前缀 `/admin/api`。所有响应使用 `{ "data": ..., "error": null }` 或 `{ "data": null, "error": {...} }`。

### 认证与权限

- `POST /auth/login`：用户名密码登录，设置 HttpOnly Cookie + CSRF Cookie。
- `POST /auth/logout`
- `GET /auth/me`
- 角色：
  - `super_admin`：全部权限，包括查看原文、SMTP 设置、管理员管理。
  - `operator`：员工、Token、Provider、模型路由、DLP 规则管理；不能查看原文。
  - `auditor`：只读 Dashboard、审计元数据；不能修改配置，不能查看原文。
- 所有写操作校验 CSRF；登录态 12 小时，滑动续期。
- 管理操作写 `admin_audit_logs`。

### 资源接口

- `GET/POST/PUT/DELETE /employees`
- `GET/POST/PUT /tokens`；`POST /tokens/:id/revoke`；`POST /tokens/:id/rotate`
  - 创建时返回一次完整 Key。
  - 读取永不返回完整 Key。
- `GET/POST/PUT/DELETE /providers`；`POST /providers/:id/test`
  - API Key 只写；读取返回 `last4`。
- `GET/POST/PUT/DELETE /model-routes`
- `GET/POST/PUT/DELETE /dlp-rules`；`POST /dlp-rules/test`
- `GET /audit`：分页、按时间/员工/Token/状态码/DLP 动作/模型过滤。
- `GET /audit/:id`：元数据，不含原文。
- `POST /audit/:id/reveal`：超级管理员；body `{ "reason": "..." }`；返回临时解密内容。
- `GET /dashboard/overview`、`GET /dashboard/trend`、`GET /dashboard/top-users`、`GET /dashboard/dlp`
- `GET/PUT /settings/smtp`；`POST /settings/smtp/test`
- `GET/POST/PUT /admin-users`（仅超级管理员）
- `GET /healthz`、`GET /readyz`（无需登录；readyz 检查数据库）
- `GET /metrics`（Prometheus；只暴露聚合指标，不暴露 Prompt）

分页使用 `limit` + `cursor`（基于 `event_time,id`），避免深分页。列表接口默认 `limit=50`，最大 200。

## 11. 管理后台页面

1. **登录**：用户名、密码、错误提示、限流提示。
2. **概览 Dashboard**：今日调用量/Token/活跃员工/DLP 命中、7/30 天趋势、Top 员工、Top DLP 规则、最近告警。
3. **团队管理**：员工列表、搜索、新增/编辑、禁用；部门侧栏支持新建、重命名，删除时可批量转移员工。
4. **Token 管理**：列表（员工、前缀、额度、周期、已用、状态、过期时间）、创建、编辑、撤销、轮换；创建成功弹窗只展示一次完整 Key，带复制按钮和“我已保存”确认。
5. **上游管理**：Provider 列表、协议、Base URL、Key 末四位、默认标记、启用/禁用、连通性测试。
6. **模型路由**：规则列表、匹配类型、目标 Provider、上游模型名、优先级、启用。
7. **DLP 规则**：内置/自定义分组，规则编辑，关键词批量导入，动作选择，测试面板（输入样例，显示命中与改写结果）。
8. **审计日志**：筛选、表格、详情抽屉；原文区域默认隐藏，超级管理员点击“查看原文”输入原因后展示；展示 DLP 命中、用量、耗时、request_id。
9. **设置**：SMTP 主机/端口/用户名/密码/发件人/收件人、告警开关、审计保留天数；发送测试邮件。
10. **管理员**：账号、角色、启用/禁用、重置密码（仅超级管理员）。

前端要求：

- Vue 3 + TypeScript + Vite + Pinia + Vue Router + Element Plus + ECharts。
- 登录态用 HttpOnly Cookie，前端不存 Token；写请求带 CSRF Header。
- 所有敏感操作二次确认；原文查看后不做前端缓存。
- 表格支持服务端分页和筛选；时间为 `Asia/Shanghai` 展示。
- 移动端可用即可，不追求复杂响应式。

## 12. 安全设计

### 12.1 传输与边界

- Caddy 自动 HTTPS；强制跳转 HTTPS，启用 HSTS。
- 只开放 80/443；SSH 限制来源 IP；数据库和 Redis 只监听 Docker 内网。
- Go 服务只监听 `127.0.0.1:8080` 或 Docker 内网，不直接暴露公网。
- 管理后台可额外限制公司出口 IP（Caddy 或应用层），作为可选配置。

### 12.2 认证与授权

- 员工 Token 只存 SHA-256；比较使用常量时间比较。
- 管理员密码 Argon2id；登录限流；Session 随机 256 位，存哈希。
- Cookie：`HttpOnly`、`Secure`、`SameSite=Strict`，路径 `/`。
- CSRF：双提交 Cookie + `X-CSRF-Token`，服务端比较哈希。
- RBAC 在服务端强制执行，前端隐藏按钮只是体验优化。
- 查看原文必须 `super_admin` + reason + 审计记录。

### 12.3 数据安全

- 上游 Key、SMTP 密码、Prompt、Response 均 AES-256-GCM 加密落盘。
- 主密钥只放环境变量或腾讯云密钥管理服务；`.env` 权限 0600，不提交 Git。
- 日志脱敏：不打印 Authorization、Cookie、Prompt、Response、上游 Key。
- 错误响应不包含堆栈、SQL、内部地址。
- 数据库备份加密后上传腾讯云 COS；备份保留 7 天；恢复流程写入运维文档。
- 审计表数据库权限最小化；应用账号不能 UPDATE/DELETE 审计日志。

### 12.4 输入与请求安全

- 请求体上限 2 MiB；请求头总大小限制；URL 长度限制。
- JSON 嵌套深度限制，避免栈溢出；正则使用 RE2，避免 ReDoS。
- 所有 SQL 使用参数化查询；禁止字符串拼接。
- Provider Base URL 仅管理员可配置；默认要求 HTTPS；若允许 HTTP 仅限内网并明确提示。
- 转发时清理 hop-by-hop headers，防止请求走私和头部注入。
- CORS：管理 API 只允许同源；网关 `/v1/*` 默认不启用浏览器 CORS，按需配置。
- 不自动跟随上游重定向，避免 SSRF 和认证头泄漏。

### 12.5 依赖与运维

- Go、npm 依赖锁定版本；定期运行漏洞扫描。
- 容器使用非 root 用户；只读根文件系统（除 spool/临时目录）。
- 健康检查 + 进程守护 + 磁盘/内存告警。
- 发布前跑 `go test ./...`、前端构建、集成测试和迁移回滚演练。

## 13. 部署与运维

### 13.1 Docker Compose 服务

```text
caddy        # 443/80, TLS, 静态后台, 反代
sluice       # Go 二进制, :8080 内网
postgres     # PostgreSQL 16, 数据卷
redis        # Redis 7, 仅内网, 可无持久化
```

- Caddy 配置：
  - `handle /v1/*` → `reverse_proxy sluice:8080`，关闭响应缓冲。
  - `handle /admin/api/*`、`/portal/api/*` → `reverse_proxy sluice:8080`。
  - `handle` → 静态 `web/dist`，SPA fallback 到 `index.html`；`/admin/*` 与 `/portal/*` 由前端路由接管，站点根 `/` 跳转到 `/portal`。
- 数据卷：`pgdata`、`caddy_data`、`caddy_config`、`audit-spool`、`backups`。
- 资源建议：2 vCPU / 4 GB 内存起步；Postgres 单独数据盘更稳妥。
- 时区：容器统一 `Asia/Shanghai`；数据库存 `timestamptz`，展示按上海时区。

### 13.2 配置项

```text
LISTEN_ADDR=:8080
PUBLIC_BASE_URL=https://api.example.com
DATABASE_URL=postgres://...
REDIS_URL=redis://redis:6379/0
MASTER_KEY=<base64 32 bytes>
MASTER_KEY_PREVIOUS=<optional>
COOKIE_SECURE=true
TRUSTED_PROXIES=172.16.0.0/12
CAPTURE_LIMIT_BYTES=262144
REQUEST_BODY_LIMIT_BYTES=2097152
AUDIT_RETENTION_DAYS=180
AUDIT_SPOOL_DIR=/var/lib/sluice/spool
BOOTSTRAP_ADMIN_USERNAME=admin
BOOTSTRAP_ADMIN_PASSWORD=<首次启动用，创建后移除>
SMTP_*（也可在后台设置）
```

- 首次启动执行迁移，并用 bootstrap 管理员创建账号；若管理员已存在则忽略。
- 迁移使用 goose，启动时自动执行；生产发布前先备份。
- 健康检查：`/healthz` 进程存活；`/readyz` 检查 PostgreSQL，Redis 故障不阻塞 ready。
- 日志：JSON 结构化输出到 stdout，Docker 日志轮转；审计日志不写 stdout。
- 备份：每日 `pg_dump` + 加密上传 COS；每月做一次恢复演练。
- 监控：`/metrics` 暴露请求量、延迟、DLP 命中、审计队列长度、spool 文件数、数据库错误；可接腾讯云监控或 Prometheus。

### 13.3 故障降级

| 故障 | 行为 |
|---|---|
| Redis 不可用 | Token 直接查 PostgreSQL；限流退化为本机内存；Dashboard 缓存失效 |
| PostgreSQL 不可用 | 无法校验 Token/额度，网关返回 503；不猜测放行 |
| 审计写入失败 | 重试后写加密 spool；请求不受影响；邮件/日志告警 |
| SMTP 不可用 | 告警进入重试队列；不阻塞请求 |
| 上游不可用 | 返回 502/504，记录审计；不自动重试 |
| 磁盘满 | 停止写 spool，丢弃并告警；请求继续但审计丢失风险需立即处理 |

认证和额度是安全边界：PostgreSQL 故障时选择 **fail closed**（503），而不是放行。

### 13.4 上线切换

- 上线前在后台录入上游 Provider 和真实 Key，创建员工与 Token。
- 发布当天撤销所有此前自由分发的上游 Key。旧 Key 不撤销，员工就可以绕过网关，DLP 和审计目标不成立。
- 撤销时机：网关完成端到端验证（三个上游各一次流式 + 非流式）后再撤销，避免切换窗口内无法工作。
- 通知员工更新 `base_url` 和 API Key；提供 OpenAI SDK / Claude SDK 的最小配置样例。
- 保留 1–2 周观察期，对比上游账号侧调用量与网关统计。两者明显不一致，说明仍有绕过流量，需要继续排查。
- 如果业务上不能立即撤销旧 Key，必须明确这是临时状态并设定截止日期，不能长期保持双轨。

## 14. 测试与验收

### 14.1 自动化测试

- 单元测试：
  - DLP：内置规则命中/误报、Luhn 校验、区间合并、Mask 改写、Block 优先级、关键词导入。
  - Token：生成、哈希、过期、禁用、IP 白名单、周期重置。
  - 配额：预留、结算、退还、并发竞争、402。
  - 路由：路径协议判断、模型前缀/精确/通配、默认 Provider、上游模型替换。
  - SSE：分块透传、usage 提取、截断、客户端断连。
  - 加密：AES-GCM 往返、AAD 不匹配失败、key id 选择。
- 集成测试（`httptest` + 模拟上游）：
  - 非流式与流式转发。
  - Block / Mask / Audit 三种 DLP 路径。
  - 401 / 402 / 403 / 429 / 502 / 504。
  - Redis 断开后的降级。
  - 审计队列满时 spool 写入与回放。
  - 管理员登录、CSRF、RBAC、原文查看审计。
- 端到端：
  - 首日模型：OpenAI、Claude、DeepSeek 各一个聊天模型，使用真实测试 Key；具体模型名在部署前由管理员填入 Provider / 模型路由。
  - 用真实 OpenAI / Anthropic / DeepSeek 测试账号各跑一次流式和非流式。
  - 用 OpenAI SDK、Anthropic SDK 分别指向网关，确认无需改业务代码。Anthropic SDK 默认发送 `x-api-key`，网关必须接受该头。

### 14.2 性能验收

- 5000 次/天不是压力，但要验证峰值：100 并发 SSE 流持续 1 分钟。
- 网关附加延迟 p95 ≤ 10ms（不含上游；DLP 典型请求 ≤ 5ms）。
- 审计异步写入不增加请求延迟；队列积压可观测。
- 内存稳定，无 goroutine 泄漏；流式连接断开后上游请求被取消。

### 14.3 安全验收

- 无效/过期/禁用 Token 全部 401。
- IP 白名单外请求被拒绝。
- 上游 Key 无法通过任何管理 API 读取明文。
- Prompt/Response 在数据库中为密文；直接查库不可读。
- 查看原文产生管理员审计记录。
- 审计日志无法通过应用账号 UPDATE/DELETE。
- 请求体超限、深层 JSON、恶意正则不会导致进程崩溃。

## 15. 实施阶段

### 阶段 0：项目骨架与基础设施

- Go module、目录结构、配置加载、结构化日志、优雅关闭。
- Docker Compose、Caddy、PostgreSQL、Redis、迁移框架。
- 数据库表、初始迁移、bootstrap 管理员。
- 管理员登录、Session、CSRF、RBAC 基础。

### 阶段 1：核心网关

- Token 生成/校验/撤销/过期/IP 白名单。
- Provider、模型路由、上游 Key 加密。
- 非流式与 SSE 代理；OpenAI / Anthropic / DeepSeek 适配。
- 配额预留/结算、RPM 限流、错误码。
- 异步审计队列、加密落库、Dashboard 基础查询。

### 阶段 2：DLP 与告警

- 规则引擎、内置规则、自定义规则、测试接口。
- Block / Mask / Audit 全链路。
- 邮件告警、去重、SMTP 设置。
- 审计详情与原文查看（超级管理员）。

### 阶段 3：管理后台

- Vue 3 工程、登录、布局、路由、权限。
- 员工、Token、Provider、模型路由、DLP 规则页面。
- Dashboard 图表、审计列表/详情、设置页。

### 阶段 4：上线加固

- 集成测试、并发测试、安全自查。
- 备份/恢复演练、监控告警、日志轮转。
- 腾讯云部署、域名/证书、防火墙。
- 员工接入文档（OpenAI SDK / Claude SDK 配置示例）。
- 旧上游 Key 撤销、员工 Token 发放、切换通知和观察期。

## 16. 留给构建者的判断

以下问题不影响产品形态，由构建者按工程常识决定：

- 具体 Go 库选择（Gin 之外的中间件、pgx、goose、Argon2id 实现）。
- 审计事件的具体 JSON 结构和内部接口命名。
- DLP 正则的具体写法与测试样例；内置规则可迭代。
- 前端组件拆分和视觉细节；遵循 Element Plus 默认风格。
- Docker 镜像基础镜像、构建缓存、CI 具体配置。
- 日志字段命名、Prometheus 指标命名。
- 默认预留 Token 数（建议 4096）、默认 RPM（建议 60）。
- 审计保留天数（建议 180 天）、邮件去重窗口（建议 5 分钟）。
- 管理员 Session 时长（建议 12 小时）。

需要产品负责人确认但当前已有默认值的事项：

- 新员工默认额度：建议每月 1,000,000 tokens，管理员可改。
- 额度 80% 是否发邮件：建议默认开启，可在设置中关闭。
- 是否允许 HTTP 上游：默认只允许 HTTPS，内网自建模型需显式开启。
- 审计原文保留 180 天：可在设置中调整。

## 17. 术语

- **上游**：OpenAI、Anthropic、DeepSeek 或企业自建大模型服务。
- **公司 Token**：`sk-company-*`，员工持有，只用于访问网关。
- **上游 Key**：真实供应商 API Key，只保存在服务端加密字段。
- **DLP**：Data Loss Prevention，数据泄露防护。
- **Block / Mask / Audit**：阻断 / 脱敏 / 仅审计。
- **SSE**：Server-Sent Events，大模型流式输出协议。
- **spool**：审计写入失败时的本地加密暂存文件。
