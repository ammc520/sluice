# 企业 API 安全网关与 DLP 监测系统实施计划

**Goal:** 建成一个生产可用的统一大模型 API 网关，支持公司 Token、配额、IP/速率限制、OpenAI/Anthropic/DeepSeek 转发、DLP 阻断/脱敏、异步审计和管理后台。

**Architecture:** 单个 Go（Gin）服务同时提供 `/v1/*` 代理和 `/admin/api/*`；Vue 3 管理后台由 Caddy 托管；PostgreSQL 保存元数据、额度和审计日志；Redis 做 Token 缓存与限流，可降级；审计通过内存队列异步写入，敏感字段 AES-256-GCM 加密。

**Tech Stack:** Go 1.21+、Gin、pgx/v5、goose、Redis 7、PostgreSQL 16、Vue 3 + TypeScript + Vite + Element Plus + ECharts、Vitest、Docker Compose、Caddy。

**Spec:** `docs/design.md`

## Global Constraints

- 单机 Docker Compose 部署；不引入 Kubernetes、ClickHouse、SSO、协议转换。
- 第一版只支持纯文本：`POST /v1/chat/completions`、`POST /v1/completions`、`POST /v1/embeddings`、`GET /v1/models`、`POST /v1/messages`、`POST /v1/messages/count_tokens`；其余 `/v1/*` 返回 501，图片/文件/音频 content block 返回 415。
- 请求体上限 2 MiB；审计 Prompt/Response 每字段最多留存 256 KiB，超出截断并标记。
- 网关附加延迟 p95 ≤ 10 ms（不含上游）；DLP 典型请求 p95 ≤ 5 ms。
- 请求主路径禁止同步写 PostgreSQL；审计只能通过有界 channel 异步投递。
- 公司 Token 只存 SHA-256 摘要；上游 Key、SMTP 密码、Prompt、Response 使用 AES-256-GCM 加密落盘。
- PostgreSQL 故障时认证/额度 fail closed（503）；Redis 故障时降级到 PostgreSQL + 本机限流。
- 状态码：401 认证失败、402 额度耗尽、403 DLP Block、413 请求体过大、415 不支持的内容、429 限流、501 不支持的端点、502 上游不可用、504 上游超时。
- 额度周期按 `Asia/Shanghai`；默认 RPM 60；生成请求默认预留 4096 tokens；管理员 Session 12 小时；审计保留 180 天；邮件告警去重 5 分钟。
- 管理后台仅独立管理员账号；角色为 `super_admin`、`operator`、`auditor`；查看原文仅 `super_admin`，必须填写原因并写管理员审计日志。
- OpenAI 与 Anthropic 协议严格分开；`model_routes.protocol` 必须与路径协议一致。
- 所有时间存 `timestamptz`，界面按上海时区展示。
- 不记录明文 Token、上游 Key、Cookie、密码；错误响应不返回内部细节。

## Review Focus

- **SSE 用量缺失/分块边界**：流式响应被拆成任意 chunk 时，usage 解析不能漏计；缺失 usage 时必须按预留结算并标记 `usage_estimated=true`。
- **客户端中途断开**：必须取消上游请求、释放 goroutine，并记录 `client_disconnected=true`，不能留下悬挂连接。
- **Redis 故障**：Token 校验、额度、限流仍可用（数据库 + 本机限流），不能整体 500。
- **审计写入失败/队列满**：请求延迟不受影响；事件写入加密 spool 并可回放，不能静默丢失。
- **DLP Mask 破坏请求**：重叠命中、多个规则、JSON 控制字段和二进制字段不能被错误改写；Block 必须优先于 Mask/Audit。

## File Map

```text
cmd/gateway/main.go                 # 进程入口、依赖装配、优雅关闭
internal/config/                    # 环境变量加载与校验
internal/server/                    # Gin 引擎、路由挂载、健康检查
internal/store/                     # 领域模型与存储接口
internal/store/postgres/            # pgx 实现
internal/crypto/                    # AES-GCM、Token 生成/哈希、密码哈希
internal/quota/                     # 额度预留/结算业务规则
internal/ratelimit/                 # Redis 令牌桶 + 内存降级
internal/auth/                      # 员工 Token 认证、Principal 缓存
internal/adminauth/                 # 管理员会话、CSRF、RBAC
internal/adminapi/                  # 管理 API handlers
internal/proxy/                     # 路由解析、上游转发、SSE、usage 解析
internal/dlp/                       # 规则引擎、内置规则、JSON 改写
internal/gateway/                   # /v1 请求编排
internal/audit/                     # 审计事件、异步 writer、spool
internal/alert/                     # SMTP 邮件告警
internal/testutil/                  # 测试数据库辅助
migrations/                         # goose SQL 迁移
web/                                # Vue 3 管理后台
deploy/                             # Dockerfile、Caddyfile、备份脚本
docs/operations/                    # 上线、恢复、员工接入文档
```

---

### Task 1: 项目骨架、配置、健康检查

**Files:**
- Create: `go.mod`, `cmd/gateway/main.go`, `internal/config/config.go`, `internal/config/config_test.go`, `internal/server/server.go`, `internal/server/server_test.go`, `Makefile`, `.gitignore`, `.env.example`, `compose.dev.yml`

**Interfaces:**
- Produces:
  - `config.Load() (*config.Config, error)`
  - `config.Config` 字段：`ListenAddr string`、`PublicBaseURL string`、`DatabaseURL string`、`AuditMaintenanceDatabaseURL string`、`RedisURL string`、`MasterKey []byte`、`MasterKeyPrevious []byte`、`CookieSecure bool`、`TrustedProxies []string`、`RequestBodyLimit int64`、`CaptureLimit int`、`AuditRetentionDays int`、`AuditSpoolDir string`
  - `server.New(cfg *config.Config, deps server.Dependencies) *server.Server`
  - `server.Dependencies{Ready func(context.Context) error, Logger *slog.Logger}`
  - `(*server.Server).Handler() http.Handler`
  - `(*server.Server).Run(ctx context.Context) error`
  - `GET /healthz` 返回 200；`GET /readyz` 在 `Ready` 返回错误时 503。

- [ ] **Step 1: 初始化 Go module 和开发依赖**

```bash
cd D:/work/api-key
git init -b main
go mod init github.com/company/api-key
go get github.com/gin-gonic/gin@v1.10.0
go get github.com/jackc/pgx/v5@v5.7.1
go get github.com/google/uuid@v1.6.0
go get github.com/redis/go-redis/v9@v9.7.0
go get github.com/pressly/goose/v3@v3.22.1
go get golang.org/x/crypto@v0.31.0
```

`.gitignore` 至少包含：`.env`、`bin/`、`web/node_modules/`、`web/dist/`、`coverage.out`、`*.spool`、`spool/`。

- [ ] **Step 2: 写配置失败与健康检查的失败测试**

`internal/config/config_test.go`：

```go
func TestLoadRejectsMissingMasterKey(t *testing.T) {
    t.Setenv("DATABASE_URL", "postgres://x")
    _, err := Load()
    if err == nil || !strings.Contains(err.Error(), "MASTER_KEY") {
        t.Fatalf("expected MASTER_KEY error, got %v", err)
    }
}

func TestLoadDecodes32ByteMasterKey(t *testing.T) {
    key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
    t.Setenv("MASTER_KEY", key)
    t.Setenv("DATABASE_URL", "postgres://x")
    cfg, err := Load()
    if err != nil { t.Fatal(err) }
    if len(cfg.MasterKey) != 32 { t.Fatalf("got %d bytes", len(cfg.MasterKey)) }
}
```

`internal/server/server_test.go`：

```go
func TestHealthz(t *testing.T) {
    s := New(&config.Config{}, Dependencies{Ready: func(context.Context) error { return nil }})
    rr := httptest.NewRecorder()
    s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
    if rr.Code != 200 { t.Fatalf("got %d", rr.Code) }
}

func TestReadyzFailsClosed(t *testing.T) {
    s := New(&config.Config{}, Dependencies{Ready: func(context.Context) error { return errors.New("db down") }})
    rr := httptest.NewRecorder()
    s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))
    if rr.Code != 503 { t.Fatalf("got %d", rr.Code) }
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/config ./internal/server -v`
Expected: FAIL，`Load`/`New` 未定义。

- [ ] **Step 4: 实现配置、Server 与入口**

`config.Load` 使用 `os.Getenv`；必填 `DATABASE_URL`、`MASTER_KEY`；`MASTER_KEY` 必须是 base64 编码的 32 字节；`MasterKeyPrevious` 可为空但若非空也必须 32 字节；`AUDIT_MAINTENANCE_DATABASE_URL` 可选，未设置时保留任务禁用并记录警告；默认值：`LISTEN_ADDR=:8080`、`REQUEST_BODY_LIMIT_BYTES=2097152`、`CAPTURE_LIMIT_BYTES=262144`、`AUDIT_RETENTION_DAYS=180`、`AUDIT_SPOOL_DIR=./spool`、`COOKIE_SECURE=true`。
`server.New` 创建 Gin engine，挂载 `/healthz`、`/readyz`，使用 `http.Server` 并设置 `ReadHeaderTimeout=10s`、`IdleTimeout=120s`。`Run` 监听 `SIGINT`/`SIGTERM`，`Shutdown` 超时 10 秒。
`cmd/gateway/main.go` 加载配置、构造 `slog` JSON logger、调用 `Run`。

`compose.dev.yml` 提供 `postgres:16`（用户/密码/库均为 `gateway`，端口 5432）和 `redis:7`（端口 6379），用于本地开发和测试。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/config ./internal/server -v`
Expected: PASS。

- [ ] **Step 6: 初始提交**

```bash
git add go.mod go.sum cmd internal Makefile .gitignore .env.example compose.dev.yml docs
git commit -m "chore: bootstrap gateway server and configuration"
git checkout -b feat/api-security-gateway
```

---

### Task 2: 数据库 Schema、领域模型与存储接口

**Files:**
- Create: `migrations/0001_init.sql`, `migrations/embed.go`, `internal/store/models.go`, `internal/store/store.go`, `internal/store/postgres/store.go`, `internal/testutil/db.go`, `internal/store/postgres/migrate_test.go`, `internal/store/postgres/helpers_test.go`

**Interfaces:**
- Produces:
  - 迁移嵌入函数：`migrations.FS() embed.FS`
  - `postgres.Open(ctx, databaseURL string) (*Store, error)`
  - `postgres.Migrate(ctx, pool *pgxpool.Pool) error`
  - `store.Employee`、`store.Token`、`store.Provider`、`store.ModelRoute`、`store.DLPRule`、`store.AuditRecord`、`store.AdminUser`、`store.AdminSession`
  - `store.Reservation{TokenID uuid.UUID; PeriodStart time.Time; Reserved int64}`
  - 错误：`store.ErrNotFound`、`store.ErrQuotaExceeded`
  - 接口：`EmployeeStore`、`TokenStore`、`ProviderStore`、`RouteStore`、`DLPRuleStore`、`AuditStore`、`AdminStore`、`SettingsStore`
  - `(*postgres.Store)` 实现以上全部接口；`testutil.OpenTestDB(t *testing.T) *pgxpool.Pool`，使用 `TEST_DATABASE_URL`，未设置时 `t.Skip`，每个测试前清理数据。

- [ ] **Step 1: 写迁移幂等测试**

```go
func TestMigrationsUpDownUp(t *testing.T) {
    pool := testutil.OpenTestDB(t)
    ctx := context.Background()
    if err := Migrate(ctx, pool); err != nil { t.Fatal(err) }
    if err := Migrate(ctx, pool); err != nil { t.Fatal("second up failed:", err) }
    var n int
    if err := pool.QueryRow(ctx, "SELECT count(*) FROM tokens").Scan(&n); err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `TEST_DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway_test?sslmode=disable go test ./internal/store/postgres -run TestMigrationsUpDownUp -v`
Expected: FAIL，`Migrate`/`OpenTestDB` 未定义。

- [ ] **Step 3: 写迁移与领域模型**

`migrations/0001_init.sql` 逐字实现设计文档 §9 的 DDL，包括 `model_routes.protocol`，并加入：
- `tokens` 的 `token_hash CHAR(64) UNIQUE`、`quota_total BIGINT NULL`。
- `audit_logs` 的索引与 §9 一致。
- `admin_sessions`、`admin_audit_logs`、`settings`。
- `model_routes.match_type` 默认 `prefix`。

`internal/store/models.go` 定义（字段类型必须与 SQL 对应）：

```go
type Employee struct {
    ID uuid.UUID; EmployeeNo, Name, Email string
    DepartmentID *int64; Status int16
    CreatedAt, UpdatedAt time.Time
}
type Token struct {
    ID, EmployeeID uuid.UUID
    TokenHash, TokenPrefix, TokenLast4 string
    QuotaType, QuotaPeriod string
    QuotaTotal *int64; QuotaUsed int64
    PeriodStart *time.Time; RateLimitRPM int
    IPAllowlist []string; Status int16
    ExpiresAt *time.Time; CreatedAt time.Time; LastUsedAt *time.Time
}
type Provider struct {
    ID uuid.UUID; Name, Protocol, BaseURL string
    APIKeyEnc []byte; IsDefault, Enabled bool
    TimeoutSeconds int; CreatedAt, UpdatedAt time.Time
}
type ModelRoute struct {
    ID, ProviderID uuid.UUID; Protocol, Pattern, MatchType string
    UpstreamModel *string; Priority int; Enabled bool
}
type DLPRule struct {
    ID uuid.UUID; Name, Description, Category, MatchType, Pattern, Action, Replacement string
    Priority int; Enabled, Builtin, AlertEnabled bool
}
type AuditRecord struct {
    ID int64; EventTime time.Time; RequestID string
    TokenID, EmployeeID *uuid.UUID; DepartmentID *int64
    ClientIP, Method, Path, Model string; ProviderID *uuid.UUID
    StatusCode int; ErrorCode string
    LatencyMS, UpstreamLatencyMS, FirstByteMS int
    PromptTokens, CompletionTokens, TotalTokens int
    UsageEstimated bool; PromptEnc, ResponseEnc []byte
    PromptTruncated, ResponseTruncated bool
    DLPAction string; DLPHits []byte
    ClientDisconnected bool; UpstreamRequestID string
}
type AdminUser struct {
    ID uuid.UUID; Username, PasswordHash, Role string
    Status int16; LastLoginAt *time.Time; CreatedAt time.Time
}
type AdminSession struct {
    ID, AdminID uuid.UUID; TokenHash, CSRFHash string
    ExpiresAt, CreatedAt, LastSeenAt time.Time
}
type AdminAuditLog struct {
    ID int64; AdminID uuid.UUID; Action, TargetType, TargetID string
    Detail []byte; ClientIP string; CreatedAt time.Time
}
```

`internal/store/store.go` 定义接口。关键签名固定为：

```go
type TokenStore interface {
    Create(ctx context.Context, t *Token) error
    Get(ctx context.Context, id uuid.UUID) (*Token, error)
    FindByHash(ctx context.Context, hash string) (*Token, error)
    Update(ctx context.Context, t *Token) error
    List(ctx context.Context, f TokenFilter) ([]Token, int, error)
    ReserveQuota(ctx context.Context, id uuid.UUID, reserve int64, now time.Time) (Reservation, error)
    SettleQuota(ctx context.Context, r Reservation, actual int64) error
    Touch(ctx context.Context, id uuid.UUID, at time.Time) error
}
type EmployeeStore interface {
    Create(ctx context.Context, e *Employee) error
    Get(ctx context.Context, id uuid.UUID) (*Employee, error)
    Update(ctx context.Context, e *Employee) error
    List(ctx context.Context, f EmployeeFilter) ([]Employee, int, error)
}
type ProviderStore interface { Create(context.Context, *Provider) error; Get(context.Context, uuid.UUID) (*Provider, error); List(context.Context) ([]Provider, error); Update(context.Context, *Provider) error; Delete(context.Context, uuid.UUID) error }
type RouteStore interface { Create(context.Context, *ModelRoute) error; List(context.Context) ([]ModelRoute, error); ListEnabled(context.Context) ([]ModelRoute, error); Delete(context.Context, uuid.UUID) error }
type DLPRuleStore interface { Create(context.Context, *DLPRule) error; Get(context.Context, uuid.UUID) (*DLPRule, error); List(context.Context) ([]DLPRule, error); ListEnabled(context.Context) ([]DLPRule, error); Update(context.Context, *DLPRule) error; Delete(context.Context, uuid.UUID) error }
type AuditStore interface {
    InsertBatch(ctx context.Context, records []*AuditRecord) error
    List(ctx context.Context, f AuditFilter) ([]*AuditRecord, error)
    Get(ctx context.Context, id int64) (*AuditRecord, error)
    Overview(ctx context.Context, from, to time.Time) (DashboardOverview, error)
    Trend(ctx context.Context, from, to time.Time) ([]TrendPoint, error)
    TopUsers(ctx context.Context, from, to time.Time, limit int) ([]UserUsage, error)
    DLPStats(ctx context.Context, from, to time.Time) ([]DLPStat, error)
    DeleteBefore(ctx context.Context, before time.Time) (int64, error)
}
type AdminStore interface {
    FindByUsername(ctx context.Context, username string) (*AdminUser, error)
    Get(ctx context.Context, id uuid.UUID) (*AdminUser, error)
    Create(ctx context.Context, u *AdminUser) error
    Update(ctx context.Context, u *AdminUser) error
    List(ctx context.Context) ([]AdminUser, error)
    CreateSession(ctx context.Context, s *AdminSession) error
    FindSessionByHash(ctx context.Context, hash string) (*AdminSession, error)
    TouchSession(ctx context.Context, id uuid.UUID, at time.Time) error
    DeleteSession(ctx context.Context, hash string) error
    DeleteExpiredSessions(ctx context.Context, before time.Time) error
    InsertAdminAudit(ctx context.Context, a *AdminAuditLog) error
}
type SettingsStore interface {
    Get(ctx context.Context, key string) (json.RawMessage, error)
    Set(ctx context.Context, key string, value json.RawMessage) error
}
```
`AuditFilter{From, To *time.Time; EmployeeID *uuid.UUID; StatusCode *int; DLPAction, Model string; Cursor string; Limit int}`；`DashboardOverview{TotalCalls, ActiveUsers, TotalTokens, DLPHits, ErrorCalls int64}`；`TrendPoint{Day time.Time; Calls, Tokens int64}`；`UserUsage{EmployeeID uuid.UUID; Name string; Calls, Tokens int64}`；`DLPStat{RuleName, Action string; Hits int64}`。

`TokenFilter{EmployeeID *uuid.UUID; Status *int16; Limit, Offset int}`；`EmployeeFilter{Query string; DepartmentID *int64; Limit, Offset int}`。

- [ ] **Step 4: 实现迁移运行器与测试数据库辅助**

`migrations/embed.go` 使用 `//go:embed *.sql`。`postgres.Migrate` 使用 goose 的 `Up`，`goose.SetBaseFS`。
`testutil.OpenTestDB` 连接 `TEST_DATABASE_URL`，执行迁移，测试前执行 `TRUNCATE ... RESTART IDENTITY CASCADE`（按依赖顺序或单条 `TRUNCATE` 所有表）。
`internal/store/postgres/helpers_test.go` 提供 `newTestStore(t)`、`seedEmployee(t,s)`、`seedToken(t,total,used)`、`seedProvider(t,s)`；后续存储测试复用，不重复造 fixture。

- [ ] **Step 5: 运行测试确认通过**

Run: `TEST_DATABASE_URL=... go test ./internal/store/postgres -run TestMigrationsUpDownUp -v`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add migrations internal/store internal/testutil
git commit -m "feat: add schema, domain models, and store interfaces"
```

---

### Task 3: 加密、Token 生成与密码哈希

**Files:**
- Create: `internal/crypto/envelope.go`, `internal/crypto/token.go`, `internal/crypto/password.go`, `internal/crypto/envelope_test.go`, `internal/crypto/token_test.go`, `internal/crypto/password_test.go`

**Interfaces:**
- Produces:
  - `crypto.NewCipher(current, previous []byte) (*Cipher, error)`
  - `(*Cipher).Seal(plaintext []byte, aad string) ([]byte, error)`
  - `(*Cipher).Open(envelope []byte, aad string) ([]byte, error)`
  - `crypto.GenerateToken() (plain, hash, prefix, last4 string, err error)`
  - `crypto.HashToken(plain string) string`
  - `crypto.HashPassword(password string) (string, error)`
  - `crypto.VerifyPassword(encoded, password string) bool`

- [ ] **Step 1: 写失败测试**

```go
func TestCipherRoundTripAndAAD(t *testing.T) {
    key := bytes.Repeat([]byte{1}, 32)
    c, _ := NewCipher(key, nil)
    enc, err := c.Seal([]byte("secret"), "req:prompt")
    if err != nil { t.Fatal(err) }
    got, err := c.Open(enc, "req:prompt")
    if err != nil || string(got) != "secret" { t.Fatalf("got %q err %v", got, err) }
    if _, err := c.Open(enc, "req:response"); err == nil { t.Fatal("expected AAD mismatch") }
}

func TestTokenFormatAndHash(t *testing.T) {
    plain, hash, prefix, last4, err := GenerateToken()
    if err != nil { t.Fatal(err) }
    if !strings.HasPrefix(plain, "sk-company-") { t.Fatal(plain) }
    if !strings.HasPrefix(plain, prefix) || !strings.HasSuffix(plain, last4) { t.Fatal("prefix/last4 mismatch") }
    if hash != HashToken(plain) || len(hash) != 64 { t.Fatal("hash mismatch") }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/crypto -v`
Expected: FAIL，函数未定义。

- [ ] **Step 3: 实现**

信封格式：`version(1)=1 | key id(1)=0/1 | nonce(12) | ciphertext+tag`。当前 key id 为 0，previous 为 1；`Open` 根据 key id 选择 key。`Seal` 对 `aad` 使用 `cipher.AEAD` 的 additional data。
Token：`sk-company-` + `base64.RawURLEncoding` 编码 32 字节 `crypto/rand`；hash 为 SHA-256 小写 hex；prefix 为明文前 15 字符，last4 为后 4 字符。
密码：Argon2id，参数 `time=1, memory=64*1024, threads=4, keyLen=32`，格式 `argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/crypto -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/crypto
git commit -m "feat: add envelope encryption, token generation, and password hashing"
```

---

### Task 4: 员工与 Token 存储、额度原子操作

**Files:**
- Create: `internal/store/postgres/employees.go`, `internal/store/postgres/tokens.go`, `internal/store/postgres/employees_test.go`, `internal/store/postgres/tokens_test.go`

**Interfaces:**
- Consumes: Task 2 的模型/接口、Task 3 的 `HashToken`（测试中使用）。
- Produces: `*postgres.Store` 实现 `EmployeeStore`、`TokenStore`；`ReserveQuota`/`SettleQuota` 语义见下。

- [ ] **Step 1: 写失败测试**

```go
func TestReserveQuotaRejectsExhausted(t *testing.T) {
    s, tok := seedToken(t, 100, 0) // quota_total=100, used=0
    if _, err := s.ReserveQuota(ctx, tok.ID, 100, now); err != nil { t.Fatal(err) }
    if _, err := s.ReserveQuota(ctx, tok.ID, 1, now); !errors.Is(err, store.ErrQuotaExceeded) { t.Fatalf("got %v", err) }
}
func TestSettleQuotaRefundsDifference(t *testing.T) {
    s, tok := seedToken(t, 100, 0)
    r, _ := s.ReserveQuota(ctx, tok.ID, 60, now)
    if err := s.SettleQuota(ctx, r, 20); err != nil { t.Fatal(err) }
    got, _ := s.Get(ctx, tok.ID)
    if got.QuotaUsed != 20 { t.Fatalf("got %d", got.QuotaUsed) }
}
func TestReserveQuotaResetsMonthlyPeriod(t *testing.T) {
    s, tok := seedToken(t, 10, 10)
    tok.PeriodStart = ptr(now.AddDate(0, -1, 0))
    r, err := s.ReserveQuota(ctx, tok.ID, 1, now)
    if err != nil { t.Fatal(err) }
    got, _ := s.Get(ctx, tok.ID)
    if got.QuotaUsed != 1 || !r.PeriodStart.Equal(monthStart(now)) { t.Fatalf("used=%d start=%v", got.QuotaUsed, r.PeriodStart) }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `TEST_DATABASE_URL=... go test ./internal/store/postgres -run 'TestReserveQuota|TestSettleQuota' -v`
Expected: FAIL，方法未实现。

- [ ] **Step 3: 实现员工与 Token 存储**

- `Create`/`Get`/`Update`/`List` 使用参数化 SQL；`List` 返回总数和分页结果。
- `FindByHash` 按 `token_hash` 精确查询。
- `ReserveQuota`：
  1. 事务内 `SELECT ... FOR UPDATE`。
  2. 若 `quota_period` 为 `daily`/`monthly` 且 `period_start` 已跨周期，`quota_used=0, period_start=周期起点`；周期边界用 `time.LoadLocation("Asia/Shanghai")` 计算当地日初/月初。
  3. `quota_total != nil` 且 `quota_used + reserve > quota_total` → `ErrQuotaExceeded`。
  4. 若 `reserve == 0` 且 `quota_total != nil` 且 `quota_used >= quota_total` → `ErrQuotaExceeded`。
  5. `quota_used += reserve`，返回 `Reservation{TokenID, PeriodStart, Reserved: reserve}`。
  6. `period_start` 为 nil 时初始化为当前周期起点。
- `SettleQuota`：事务内 `SELECT ... FOR UPDATE`；若 `period_start` 已不等于 `r.PeriodStart`，说明跨周期，忽略调整；否则 `quota_used = max(0, quota_used - r.Reserved + actual)`。
- `Touch` 更新 `last_used_at`。

- [ ] **Step 4: 运行测试确认通过**

Run: `TEST_DATABASE_URL=... go test ./internal/store/postgres -run 'TestReserveQuota|TestSettleQuota|TestToken' -v`
Expected: PASS，包括并发测试：两个 goroutine 同时预留 60/60（总额 100），只有一个成功。

- [ ] **Step 5: 提交**

```bash
git add internal/store/postgres
git commit -m "feat: implement employee and token stores with atomic quota"
```

---

### Task 5: Provider、模型路由、DLP 规则与审计存储

**Files:**
- Create: `internal/store/postgres/providers.go`, `internal/store/postgres/routes.go`, `internal/store/postgres/dlp_rules.go`, `internal/store/postgres/audit.go`, `internal/store/postgres/providers_test.go`, `internal/store/postgres/routes_test.go`, `internal/store/postgres/dlp_rules_test.go`, `internal/store/postgres/audit_test.go`

**Interfaces:**
- Produces: `*postgres.Store` 实现 `ProviderStore`、`RouteStore`、`DLPRuleStore`、`AuditStore`。
- Consumes: Task 2 的模型与接口。

- [ ] **Step 1: 写失败测试**

```go
func TestListEnabledRoutesOrdersByPriority(t *testing.T) {
    s := newTestStore(t)
    p := seedProvider(t, s)
    mustCreateRoute(t, s, &store.ModelRoute{ProviderID: p.ID, Protocol: "openai_compatible", Pattern: "gpt-*", MatchType: "prefix", Priority: 200, Enabled: true})
    mustCreateRoute(t, s, &store.ModelRoute{ProviderID: p.ID, Protocol: "openai_compatible", Pattern: "gpt-4o", MatchType: "exact", Priority: 10, Enabled: true})
    routes, err := s.ListEnabled(ctx)
    if err != nil { t.Fatal(err) }
    if len(routes) != 2 || routes[0].Priority != 10 { t.Fatalf("got %+v", routes) }
}
func TestProviderKeyIsStoredAsBytes(t *testing.T) {
    s := newTestStore(t)
    p := &store.Provider{Name: "p", Protocol: "openai_compatible", BaseURL: "https://example.com", APIKeyEnc: []byte{1,2,3}, Enabled: true}
    if err := s.Create(ctx, p); err != nil { t.Fatal(err) }
    got, _ := s.Get(ctx, p.ID)
    if !bytes.Equal(got.APIKeyEnc, []byte{1,2,3}) { t.Fatal("key bytes changed") }
}
func TestAuditInsertBatchListAndDeleteBefore(t *testing.T) {
    s := newTestStore(t)
    now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
    recs := []*store.AuditRecord{
        {RequestID: "r1", Path: "/v1/chat/completions", StatusCode: 200, EventTime: now},
        {RequestID: "r2", Path: "/v1/messages", StatusCode: 403, EventTime: now.Add(time.Second)},
    }
    if err := s.InsertBatch(ctx, recs); err != nil { t.Fatal(err) }
    rows, err := s.List(ctx, store.AuditFilter{Limit: 10})
    if err != nil || len(rows) != 2 { t.Fatalf("rows=%d err=%v", len(rows), err) }
    n, err := s.DeleteBefore(ctx, now.Add(time.Minute))
    if err != nil || n != 2 { t.Fatalf("deleted=%d err=%v", n, err) }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `TEST_DATABASE_URL=... go test ./internal/store/postgres -run 'TestListEnabledRoutes|TestProviderKey|TestAuditInsertBatch' -v`
Expected: FAIL。

- [ ] **Step 3: 实现 CRUD 与审计查询**

Provider/Route/DLPRule 的 `Create`、`Get`、`List`、`ListEnabled`、`Update`、`Delete` 全部参数化 SQL。`Delete` 对不存在的 ID 返回 `store.ErrNotFound`。`ListEnabled` 过滤 `enabled=true`，按 `priority ASC, created_at ASC` 排序。

`audit.go` 实现 `AuditStore`：`InsertBatch` 单条 `INSERT ... ON CONFLICT (request_id) DO NOTHING` 多值语句；`List` 按 `AuditFilter` 动态拼接参数化 WHERE，按 `event_time DESC, id DESC` 排序，`Limit` 默认 50、上限 100，使用 `(event_time, id)` 游标；`Get` 按 ID；`Overview`、`Trend`、`TopUsers`、`DLPStats` 用聚合 SQL；`DeleteBefore` 返回 `RowsAffected`。`DLPHits` 以 JSONB 读写。

- [ ] **Step 4: 运行测试确认通过**

Run: `TEST_DATABASE_URL=... go test ./internal/store/postgres -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/store/postgres
git commit -m "feat: implement provider, route, DLP rule, and audit stores"
```

---

### Task 6: 额度服务与 Redis/内存限流

**Files:**
- Create: `internal/quota/service.go`, `internal/quota/service_test.go`, `internal/ratelimit/limiter.go`, `internal/ratelimit/memory.go`, `internal/ratelimit/redis.go`, `internal/ratelimit/limiter_test.go`, `internal/ratelimit/redis_test.go`

**Interfaces:**
- Produces:
  - `quota.New(tokens store.TokenStore, defaultReserve int64, clock func() time.Time) *quota.Service`
  - `(*quota.Service).Reserve(ctx, t *store.Token, endpoint quota.Endpoint, maxTokens int64) (store.Reservation, error)`
  - `(*quota.Service).Settle(ctx, r store.Reservation, actual int64) error`
  - `quota.Endpoint` 常量：`EndpointGeneration`、`EndpointEmbedding`、`EndpointMetadata`
  - `ratelimit.Limiter` 接口：`Allow(ctx context.Context, key string, limit int) (allowed bool, retryAfter time.Duration, err error)`
  - `ratelimit.NewMemory() *Memory`
  - `ratelimit.NewRedis(client *redis.Client) *Redis`
  - `ratelimit.NewFallback(primary, fallback Limiter) *Fallback`
- Semantics: `EndpointMetadata` 返回零值预留且不调用存储；`EndpointEmbedding` 预留 0（仅检查剩余）；`EndpointGeneration` 预留 `maxTokens` 或 `defaultReserve`。

- [ ] **Step 1: 写失败测试**

```go
func TestQuotaMetadataDoesNotConsume(t *testing.T) {
    fake := &fakeTokenStore{}
    svc := quota.New(fake, 4096, time.Now)
    r, err := svc.Reserve(ctx, &store.Token{QuotaType: "tokens", QuotaTotal: ptr(int64(1))}, quota.EndpointMetadata, 0)
    if err != nil || r.Reserved != 0 || fake.reserveCalls != 0 { t.Fatalf("r=%+v calls=%d", r, fake.reserveCalls) }
}
func TestQuotaGenerationUsesDefaultReserve(t *testing.T) {
    fake := &fakeTokenStore{}
    svc := quota.New(fake, 4096, time.Now)
    if _, err := svc.Reserve(ctx, &store.Token{QuotaType: "tokens"}, quota.EndpointGeneration, 0); err != nil { t.Fatal(err) }
    if fake.lastReserve != 4096 { t.Fatalf("got %d", fake.lastReserve) }
}
func TestMemoryLimiterRefills(t *testing.T) {
    l := ratelimit.NewMemory()
    l.SetClock(func() time.Time { return now })
    if ok, _, _ := l.Allow(ctx, "t", 1); !ok { t.Fatal("first denied") }
    if ok, _, _ := l.Allow(ctx, "t", 1); ok { t.Fatal("second allowed") }
}
func TestFallbackLimiterUsesMemoryWhenRedisFails(t *testing.T) {
    primary := &failingLimiter{err: errors.New("redis down")}
    fallback := ratelimit.NewMemory()
    l := ratelimit.NewFallback(primary, fallback)
    if ok, _, err := l.Allow(ctx, "t", 1); !ok || err != nil { t.Fatalf("ok=%v err=%v", ok, err) }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/quota ./internal/ratelimit -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- 额度服务：`EndpointMetadata` 直接返回；`EndpointEmbedding` 以 `reserve=0` 调用 `ReserveQuota`；`EndpointGeneration` 计算预留值并调用。
- 内存限流：固定窗口或令牌桶，使用 `sync.Mutex` 和 `time.Now`；键 2 分钟无访问可清理。
- Redis 限流：Lua 脚本实现令牌桶，键 `rl:<key>`，TTL 120s；`retryAfter` 返回下一次可用时间。
- Fallback：primary 返回错误时调用 fallback，并记录一次降级计数（`atomic.Int64`）；primary 成功则使用 primary 结果。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/quota ./internal/ratelimit -v`
Expected: PASS。Redis 测试使用 `miniredis` 或在 `TEST_REDIS_URL` 存在时运行；未设置时跳过。

- [ ] **Step 5: 提交**

```bash
git add internal/quota internal/ratelimit
git commit -m "feat: add quota service and fallback rate limiting"
```

---

### Task 7: 管理员认证、Session、CSRF 与 RBAC

**Files:**
- Create: `internal/adminauth/service.go`, `internal/adminauth/service_test.go`, `internal/adminauth/middleware.go`, `internal/adminauth/middleware_test.go`, `internal/adminapi/response.go`, `internal/adminapi/auth.go`, `internal/adminapi/auth_test.go`, `internal/store/postgres/admin.go`, `internal/store/postgres/admin_test.go`

**Interfaces:**
- Produces:
  - `*postgres.Store` 实现 `AdminStore`。
  - `adminauth.New(admins store.AdminStore, limiter ratelimit.Limiter, clock func() time.Time, ttl time.Duration) *Service`
  - `(*Service).Login(ctx, username, password, ip string) (sessionToken, csrfToken string, user *store.AdminUser, err error)`
  - `(*Service).Validate(ctx, sessionToken string) (*store.AdminUser, *store.AdminSession, error)`
  - `(*Service).Logout(ctx, sessionToken string) error`
  - `adminauth.ValidateCSRF(session *store.AdminSession, headerToken string) bool`
  - Gin 中间件：`RequireSession(svc *Service) gin.HandlerFunc`、`RequireRole(role string) gin.HandlerFunc`、`RequireCSRF() gin.HandlerFunc`
  - Context key：`adminauth.ContextUser`、`adminauth.ContextSession`
  - HTTP handlers：`POST /admin/api/auth/login`、`POST /admin/api/auth/logout`、`GET /admin/api/auth/me`
  - Cookie 名称：`admin_session`、`csrf_token`；Session 12 小时；登录限流每 IP 5 次/分钟。

- [ ] **Step 1: 写失败测试**

```go
func TestLoginRejectsWrongPassword(t *testing.T) {
    svc, _ := newAuthFixture(t)
    _, _, _, err := svc.Login(ctx, "admin", "wrong", "1.2.3.4")
    if !errors.Is(err, adminauth.ErrInvalidCredentials) { t.Fatalf("got %v", err) }
}
func TestCSRFMismatchFails(t *testing.T) {
    session := &store.AdminSession{CSRFHash: crypto.HashToken("right")}
    if adminauth.ValidateCSRF(session, "wrong") { t.Fatal("accepted wrong CSRF") }
    if !adminauth.ValidateCSRF(session, "right") { t.Fatal("rejected correct CSRF") }
}
func TestAuditorCannotMutate(t *testing.T) {
    // 使用 RequireRole("super_admin") 包裹一个测试路由，以 auditor 身份请求应 403
}
func TestLoginHandlerSetsCookies(t *testing.T) {
    // 使用 Gin 测试路由挂载 LoginHandler；POST 正确凭证后断言 Set-Cookie 同时包含
    // admin_session(HttpOnly, SameSite=Strict) 和 csrf_token(非 HttpOnly)
}
func TestAdminSessionStoreRoundTrip(t *testing.T) {
    s := newTestStore(t)
    u := &store.AdminUser{Username: "admin", PasswordHash: "hash", Role: "super_admin", Status: 1}
    if err := s.Create(ctx, u); err != nil { t.Fatal(err) }
    sess := &store.AdminSession{AdminID: u.ID, TokenHash: "th", CSRFHash: "ch", ExpiresAt: time.Now().Add(time.Hour)}
    if err := s.CreateSession(ctx, sess); err != nil { t.Fatal(err) }
    got, err := s.FindSessionByHash(ctx, "th")
    if err != nil || got.AdminID != u.ID { t.Fatalf("got=%+v err=%v", got, err) }
    if err := s.DeleteSession(ctx, "th"); err != nil { t.Fatal(err) }
    if _, err := s.FindSessionByHash(ctx, "th"); !errors.Is(err, store.ErrNotFound) { t.Fatalf("got %v", err) }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/adminauth ./internal/store/postgres -run 'TestLogin|TestCSRF|TestAuditor|TestAdminSession' -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- `postgres/admin.go` 实现 `AdminStore`：管理员 CRUD、按用户名查询、Session 增删查与 `last_seen_at` 更新、过期 Session 清理、`admin_audit_logs` 插入；全部参数化 SQL，未命中返回 `store.ErrNotFound`。
- 登录：按用户名查管理员，`crypto.VerifyPassword`；生成 32 字节随机 session token 和 CSRF token；数据库存其 SHA-256；设置过期时间；写 `last_login_at`；返回明文。
- Session 验证：按 hash 查 session，检查过期；返回管理员和 session；更新 `last_seen_at`。
- CSRF：比较 `crypto.HashToken(headerToken)` 与 `session.CSRFHash`。
- 中间件：`RequireSession` 从 Cookie 读取，失败 401；`RequireCSRF` 对 POST/PUT/PATCH/DELETE 检查 `X-CSRF-Token`；`RequireRole` 检查角色。
- HTTP handlers：`auth.go` 解析登录 JSON、调用 `Login`，设置两个 Cookie；`logout` 删除 session 并清 Cookie；`me` 返回当前管理员。Cookie 属性：`admin_session` 为 `HttpOnly`、`Secure`（配置）、`SameSite=Strict`、`Path=/`、`MaxAge=43200`；`csrf_token` 不设 HttpOnly，供前端读取。
- `response.go` 定义统一 `{data,error}` 响应和错误码 helper，供后续管理 API 复用。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/adminauth ./internal/adminapi -run 'TestLogin|TestCSRF|TestAuditor' -v` 和 `TEST_DATABASE_URL=... go test ./internal/store/postgres -run TestAdminSession -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/adminauth internal/adminapi internal/store/postgres
git commit -m "feat: add admin sessions, CSRF, RBAC, auth endpoints, and admin store"
```

---

### Task 8: 管理 API — 员工与 Token

**Files:**
- Create: `internal/adminapi/employees.go`, `internal/adminapi/tokens.go`, `internal/adminapi/employees_test.go`, `internal/adminapi/tokens_test.go`, `internal/adminapi/helpers_test.go`

**Interfaces:**
- Consumes: Task 2 存储接口、Task 3 `crypto.GenerateToken`、Task 7 中间件与 `response.go` helper。
- Produces: Gin handlers：
  - `GET/POST /admin/api/employees`、`PUT /admin/api/employees/:id`
  - `GET/POST /admin/api/tokens`、`PUT /admin/api/tokens/:id`
  - `POST /admin/api/tokens/:id/revoke`、`POST /admin/api/tokens/:id/rotate`
  - 响应包：`{"data": ..., "error": null}` 或 `{"data": null, "error": {"code": "...", "message": "..."}}`
  - 创建 Token 返回 `{"token": "<完整 Key>", "token_id": "..."}`，仅此一次；列表/详情不返回完整 Key 或 hash。

- [ ] **Step 1: 写失败测试**

```go
func TestCreateTokenReturnsPlaintextOnce(t *testing.T) {
    env := newAdminEnv(t)
    body := `{"employee_id":"` + env.employeeID + `","quota_type":"tokens","quota_period":"monthly","quota_total":100000,"rate_limit_rpm":60}`
    rr := env.do("POST", "/admin/api/tokens", body)
    if rr.Code != 201 { t.Fatalf("got %d %s", rr.Code, rr.Body) }
    var resp struct{ Data struct{ Token string `json:"token"` } `json:"data"` }
    json.Unmarshal(rr.Body.Bytes(), &resp)
    if !strings.HasPrefix(resp.Data.Token, "sk-company-") { t.Fatal("missing plaintext token") }

    list := env.do("GET", "/admin/api/tokens", "")
    if strings.Contains(list.Body.String(), resp.Data.Token) { t.Fatal("list leaked token") }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/adminapi -run TestCreateTokenReturnsPlaintextOnce -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- `internal/adminapi/helpers_test.go` 提供 `newAdminEnv(t)`：测试 DB、迁移、创建 `super_admin`/`operator`/`auditor` 三个账号、返回可执行带 Cookie/CSRF 请求的 helper；后续管理 API 测试复用。
- 员工：创建/更新校验 `employee_no` 非空、`name` 非空、部门存在；列表支持 `q`、`department_id`、分页。
- Token：创建时生成 Token，只存 hash/prefix/last4；校验 `quota_type`、`quota_period`、`quota_total >= 0`、`rate_limit_rpm > 0`、`expires_at` 未来时间；IP allowlist 每项必须是合法 CIDR/IP。
- 撤销：`status=0`；轮换：创建新 Token 并撤销旧 Token，响应返回新完整 Key。
- 所有写操作要求 CSRF 和 `super_admin`/`operator` 角色。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/adminapi -run 'TestCreateToken|TestEmployee' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/adminapi
git commit -m "feat: add employee and token admin APIs"
```

---

### Task 9: 管理 API — Provider、模型路由与 DLP 规则

**Files:**
- Create: `internal/adminapi/providers.go`, `internal/adminapi/routes.go`, `internal/adminapi/dlp_rules.go`, `internal/adminapi/providers_test.go`, `internal/adminapi/routes_test.go`, `internal/adminapi/dlp_rules_test.go`

**Interfaces:**
- Consumes: Task 3 `Cipher`、Task 5 存储、Task 7 中间件、Task 13 `dlp.Engine`（测试接口可在 Task 13 中实现）。
- Produces:
  - `GET/POST /admin/api/providers`、`PUT/DELETE /admin/api/providers/:id`、`POST /admin/api/providers/:id/test`
  - `GET/POST /admin/api/model-routes`、`DELETE /admin/api/model-routes/:id`
  - `GET/POST /admin/api/dlp-rules`、`PUT/DELETE /admin/api/dlp-rules/:id`
  - Provider 读取只返回 `api_key_last4`，不返回密文/明文；创建/更新时 `api_key` 只写。
  - Provider Key 加密 AAD 固定为 `provider:<provider_id>`，Task 11 解密时必须使用同一 AAD。
  - 路由保存时校验 `protocol` 与 Provider 的 `protocol` 一致。
  - DLP 规则 `/test` 端点依赖规则引擎，放在 Task 13 实现。

- [ ] **Step 1: 写失败测试**

```go
func TestProviderKeyNeverReturned(t *testing.T) {
    env := newAdminEnv(t)
    body := `{"name":"openai","protocol":"openai_compatible","base_url":"https://api.openai.com","api_key":"sk-real-secret","is_default":true}`
    rr := env.do("POST", "/admin/api/providers", body)
    if rr.Code != 201 { t.Fatalf("got %d %s", rr.Code, rr.Body) }
    list := env.do("GET", "/admin/api/providers", "")
    if strings.Contains(list.Body.String(), "sk-real-secret") { t.Fatal("leaked provider key") }
    if !strings.Contains(list.Body.String(), `"api_key_last4":"cret"`) { t.Fatal(list.Body.String()) }
}
func TestRouteProtocolMustMatchProvider(t *testing.T) {
    env := newAdminEnv(t)
    env.createProvider(t, "anthropic", "anthropic")
    rr := env.do("POST", "/admin/api/model-routes", `{"provider_id":"`+env.providerID+`","protocol":"openai_compatible","pattern":"gpt-*","match_type":"prefix"}`)
    if rr.Code != 400 { t.Fatalf("got %d", rr.Code) }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/adminapi -run 'TestProviderKey|TestRouteProtocol' -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- Provider：加密 `api_key` 后存 `api_key_enc`，AAD 为 `provider:<provider_id>`；读取时解密只为计算 last4，绝不返回明文；`test` 请求 `GET <base_url>/v1/models`，把 2xx/401 视为“可达”（401 表示 Key 可能无效），其他状态返回错误摘要；不记录 Key。
- 路由：校验 `match_type`、`protocol` 与 Provider 一致、pattern 非空；保存后刷新内存 Resolver。
- DLP 规则：校验 regex 可编译、动作合法、关键词非空；`/test` 端点不在本任务实现，Task 13 负责。
- 仅 `super_admin`/`operator` 可写；`auditor` 只读。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/adminapi -run 'TestProvider|TestRoute|TestDLPRule' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/adminapi
git commit -m "feat: add provider, model route, and DLP rule admin APIs"
```

---

### Task 10: 员工 Token 认证与 Principal 缓存

**Files:**
- Create: `internal/auth/service.go`, `internal/auth/cache.go`, `internal/auth/service_test.go`, `internal/auth/cache_test.go`

**Interfaces:**
- Produces:
  - `auth.New(tokens store.TokenStore, employees store.EmployeeStore, cache Cache, trustedProxies []string, clock func() time.Time) *Service`
  - `(*Service).Authenticate(r *http.Request) (*Principal, error)`
  - `type Principal struct { Token store.Token; Employee store.Employee }`
  - `type Cache interface { Get(ctx context.Context, key string) (*Principal, error); Set(ctx context.Context, key string, p *Principal, ttl time.Duration) error; Delete(ctx context.Context, key string) error }`
  - `auth.NewRedisCache(client *redis.Client) Cache`；`auth.NewMemoryCache() Cache`
  - 错误：`ErrMissingToken`、`ErrInvalidToken`、`ErrTokenExpired`、`ErrTokenDisabled`、`ErrIPNotAllowed`、`ErrEmployeeDisabled`、`ErrCacheMiss`
  - `auth.ClientIP(r *http.Request, trusted []string) string`
- 规则：同时接受 `Authorization: Bearer sk-company-*` 和 `x-api-key: sk-company-*`；两者同时存在且不同返回 `ErrInvalidToken`；缓存键为 Token SHA-256，TTL 30 秒。

- [ ] **Step 1: 写失败测试**

```go
func TestAuthenticateAcceptsAnthropicXAPIKey(t *testing.T) {
    svc, plain := newAuthFixture(t)
    r := httptest.NewRequest("POST", "/v1/messages", nil)
    r.Header.Set("x-api-key", plain)
    p, err := svc.Authenticate(r)
    if err != nil || p.Token.TokenHash == "" { t.Fatalf("p=%+v err=%v", p, err) }
}
func TestAuthenticateRejectsIPOutsideAllowlist(t *testing.T) {
    svc, plain := newAuthFixtureWithIP(t, []string{"10.0.0.0/8"})
    r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
    r.Header.Set("Authorization", "Bearer "+plain)
    r.RemoteAddr = "192.0.2.10:1234"
    if _, err := svc.Authenticate(r); !errors.Is(err, auth.ErrIPNotAllowed) { t.Fatalf("got %v", err) }
}
func TestCacheInvalidation(t *testing.T) {
    c := auth.NewMemoryCache()
    c.Set(ctx, "k", &auth.Principal{}, time.Minute)
    c.Delete(ctx, "k")
    if _, err := c.Get(ctx, "k"); !errors.Is(err, auth.ErrCacheMiss) { t.Fatal(err) }
}
func TestAuthenticateFallsBackWhenCacheFails(t *testing.T) {
    svc, plain := newAuthFixtureWithCache(t, &failingCache{})
    r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
    r.Header.Set("Authorization", "Bearer "+plain)
    if _, err := svc.Authenticate(r); err != nil { t.Fatalf("cache failure should not fail auth: %v", err) }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/auth -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- 提取 Token：优先 `Authorization: Bearer `；否则 `x-api-key`；缺失返回 `ErrMissingToken`。
- 先查 cache；未命中查 `FindByHash`；再查员工；校验 `token.status==1`、`employee.status==1`、`expires_at`、IP allowlist。
- IP：只信任 `trustedProxies` 中的代理；解析 `X-Forwarded-For` 最右侧非可信地址；否则使用 `RemoteAddr`。CIDR 解析失败视为不匹配。
- 缓存 Principal 30 秒；管理 API 修改 Token/员工时调用 `Delete`（通过注入的 cache）。
- Redis 不可用时直接查库，不返回 500。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/auth -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/auth
git commit -m "feat: add company token authentication with cache fallback"
```

---

### Task 11: 路由解析与非流式上游代理

**Files:**
- Create: `internal/proxy/resolver.go`, `internal/proxy/proxy.go`, `internal/proxy/headers.go`, `internal/proxy/errors.go`, `internal/proxy/resolver_test.go`, `internal/proxy/proxy_test.go`

**Interfaces:**
- Produces:
  - `proxy.ProtocolForPath(path string) (string, bool)`；支持路径见 Global Constraints。
  - `proxy.NewResolver(providers []store.Provider, routes []store.ModelRoute) *Resolver`
  - `(*Resolver).Resolve(path, model string) (Target, error)`
  - `type Target struct { Provider store.Provider; Protocol string; UpstreamModel string }`
  - `proxy.New(cfg Config) *Proxy`；`Config{Client *http.Client; Cipher *crypto.Cipher; CaptureLimit int}`
  - `(*Proxy).Do(ctx context.Context, w http.ResponseWriter, req Request) (Result, error)`
  - `type Request struct { Target Target; Path, RawQuery string; Body []byte; Header http.Header; RequestID string; Stream bool }`
  - `type Result struct { StatusCode int; Usage Usage; ResponseBody []byte; UpstreamRequestID string; FirstByteMS int; ClientDisconnected bool }`
  - `proxy.ErrUpstreamUnavailable`、`proxy.ErrUpstreamTimeout`

- [ ] **Step 1: 写失败测试**

```go
func TestResolverRejectsCrossProtocolRoute(t *testing.T) {
    p := store.Provider{ID: uuid.New(), Protocol: "anthropic", BaseURL: "https://api.anthropic.com"}
    r := proxy.NewResolver([]store.Provider{p}, []store.ModelRoute{{ProviderID: p.ID, Protocol: "openai_compatible", Pattern: "claude-*", MatchType: "prefix", Enabled: true}})
    if _, err := r.Resolve("/v1/messages", "claude-sonnet"); err == nil { t.Fatal("expected rejection") }
}
func TestProxyRewritesModelAndAuthHeader(t *testing.T) {
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Header.Get("Authorization") != "Bearer upstream-key" { t.Error("wrong upstream auth") }
        body, _ := io.ReadAll(r.Body)
        if !strings.Contains(string(body), `"model":"real-model"`) { t.Errorf("model not rewritten: %s", body) }
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(`{"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`))
    }))
    defer upstream.Close()
    // 构造 Target{Provider: {BaseURL: upstream.URL, Protocol:"openai_compatible"}, UpstreamModel:"real-model"}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/proxy -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- 路由：按路径确定协议；只匹配 `enabled && protocol==pathProtocol` 的路由；精确 > 前缀 > 通配；同优先级按创建顺序；未命中用该协议 `is_default` Provider；没有则错误。
- Header：删除 `Authorization`、`x-api-key`、`Host`、`Content-Length`、`Connection`、`Transfer-Encoding`、`Proxy-*`；OpenAI 注入 `Authorization: Bearer <key>`；Anthropic 注入 `x-api-key: <key>` 和 `anthropic-version`（若客户端未提供则用 `2023-06-01`）。
- 解密 Provider Key：`Cipher.Open(provider.APIKeyEnc, "provider:"+provider.ID.String())`。
- 请求 URL：`base_url` + path + `?raw_query`；`http.Client.CheckRedirect` 返回 `http.ErrUseLastResponse`，不跟随重定向；Transport 连接超时 10s，`ResponseHeaderTimeout` 120s；流式读取空闲 300s 后取消；客户端请求上下文取消时立即取消上游。
- 非流式：把上游响应完整写给客户端，同时只捕获前 `CaptureLimit` 字节用于审计；超过时 `ResponseTruncated=true`。不得因为捕获上限而截断客户端响应。
- 状态码透传；连接错误映射 `ErrUpstreamUnavailable`；超时映射 `ErrUpstreamTimeout`。
- 解析 usage：OpenAI/Anthropic 非流式 JSON；缺失时 `Estimated=true`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/proxy -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/proxy
git commit -m "feat: add route resolver and non-streaming upstream proxy"
```

---

### Task 12: SSE 流式透传与 usage 解析

**Files:**
- Create: `internal/proxy/stream.go`, `internal/proxy/usage.go`, `internal/proxy/stream_test.go`, `internal/proxy/usage_test.go`

**Interfaces:**
- Consumes: Task 11 的 `Proxy`、`Request`、`Result`。
- Produces:
  - `(*Proxy).Stream(ctx context.Context, w http.ResponseWriter, flush func(), req Request) (Result, error)`
  - `proxy.NewStreamParser(protocol string) StreamParser`
  - `type StreamParser interface { Feed(line []byte); Usage() Usage; Done() bool }`
  - `Usage{PromptTokens, CompletionTokens, TotalTokens int; Estimated bool}`

- [ ] **Step 1: 写失败测试**

```go
func TestOpenAIStreamParserUsageAcrossChunks(t *testing.T) {
    p := proxy.NewStreamParser("openai_compatible")
    chunks := []string{
        `data: {"choices":[{"delta":{"content":"he"}}]}` + "\n\n",
        `data: {"choices":[{"delta":{"content":"llo"}}]}` + "\n\n",
        `data: {"usage":{"prompt_tokens":5,"completion_tokens":6,"total_tokens":11}}` + "\n\n",
        "data: [DONE]\n\n",
    }
    for _, c := range chunks { p.Feed([]byte(c)) }
    u := p.Usage()
    if u.TotalTokens != 11 || u.Estimated { t.Fatalf("%+v", u) }
}
func TestAnthropicStreamParserUsage(t *testing.T) {
    p := proxy.NewStreamParser("anthropic")
    p.Feed([]byte("event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":9}}}\n\n"))
    p.Feed([]byte("event: message_delta\ndata: {\"usage\":{\"output_tokens\":4}}\n\n"))
    if got := p.Usage(); got.PromptTokens != 9 || got.CompletionTokens != 4 { t.Fatalf("%+v", got) }
}
func TestStreamFlushesEachChunk(t *testing.T) {
    // 使用 httptest.Server 每次 Write 后 sleep 10ms；自定义 ResponseWriter 记录 Flush 次数，断言 >= 2
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/proxy -run 'Test.*Stream|TestAnthropic' -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- `Stream`：发送上游请求；复制状态码和 `Content-Type`；设置 `Cache-Control: no-cache`、`X-Accel-Buffering: no`；循环 `Read` 并 `w.Write` + `flush()`；捕获到 `CaptureLimit` 后继续透传但停止记录。
- HTTP client 不能启用自动 gzip 或整体缓冲；设置 `Accept-Encoding: identity`。
- 每行喂给 parser；parser 必须容忍 chunk 边界在任意位置，使用内部行缓冲。
- OpenAI parser：解析 `data:` JSON，取 `usage`；`[DONE]` 标记结束。
- Anthropic parser：解析 `event: message_start` 的 `message.usage.input_tokens` 和 `message_delta` 的 `usage.output_tokens`。
- 客户端断开：`ctx` 取消，关闭上游 body；返回 `Result.ClientDisconnected=true`。
- 响应结束仍无 usage：`Estimated=true`，由上层按预留结算。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/proxy -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/proxy
git commit -m "feat: add SSE streaming proxy and usage parsers"
```

---

### Task 13: DLP 规则引擎、内置规则与 JSON 改写

**Files:**
- Create: `internal/dlp/engine.go`, `internal/dlp/builtin.go`, `internal/dlp/rewrite.go`, `internal/dlp/engine_test.go`, `internal/dlp/builtin_test.go`, `internal/dlp/rewrite_test.go`, `internal/adminapi/dlp_test_endpoint.go`, `internal/adminapi/dlp_test_endpoint_test.go`

**Interfaces:**
- Consumes: Task 9 的 DLP 规则 CRUD、Task 7 中间件。
- Produces:
  - `dlp.NewEngine(rules []store.DLPRule) (*Engine, []error)`；无效规则跳过并返回错误列表。
  - `(*Engine).Reload(rules []store.DLPRule) []error`
  - `(*Engine).Inspect(body []byte) (Result, error)`
  - `(*Engine).Test(text string) (Result, error)`；只扫描纯文本，不改写。
  - `type Result struct { Action string; Hits []Hit; Body []byte }`
  - `type Hit struct { RuleID uuid.UUID; RuleName, Action string; AlertEnabled bool; Start, End int }`
  - `dlp.BuiltinRules() []store.DLPRule`
  - `dlp.EnsureBuiltinRules(ctx, rules store.DLPRuleStore) error`；按 name 插入缺失规则，不覆盖已有。
  - `dlp.ErrUnsupportedContent`
  - `POST /admin/api/dlp-rules/test`：接收 `{text, rules?}`，返回命中规则和脱敏后文本，不写数据库；仅 `super_admin`/`operator`。

- [ ] **Step 1: 写失败测试**

```go
func TestBlockWinsOverMask(t *testing.T) {
    e, _ := dlp.NewEngine([]store.DLPRule{
        {Name: "phone", MatchType: "regex", Pattern: `1[3-9]\d{9}`, Action: "mask", Priority: 1, Enabled: true},
        {Name: "aws", MatchType: "regex", Pattern: `AKIA[0-9A-Z]{16}`, Action: "block", Priority: 2, Enabled: true},
    })
    res, err := e.Inspect([]byte(`{"messages":[{"role":"user","content":"AKIAABCDEFGHIJKLMNOP 13800138000"}]}`))
    if err != nil { t.Fatal(err) }
    if res.Action != "block" { t.Fatalf("got %s", res.Action) }
}
func TestMaskRewritesAllHitsAndKeepsValidJSON(t *testing.T) {
    e, _ := dlp.NewEngine([]store.DLPRule{{Name: "phone", MatchType: "regex", Pattern: `1[3-9]\d{9}`, Action: "mask", Replacement: "***", Enabled: true}})
    in := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"a 13800138000 b 13900139000"}]}`)
    res, err := e.Inspect(in)
    if err != nil { t.Fatal(err) }
    var out map[string]any
    if err := json.Unmarshal(res.Body, &out); err != nil { t.Fatal(err) }
    if strings.Contains(string(res.Body), "13800138000") || !strings.Contains(string(res.Body), `"model":"gpt-4o"`) { t.Fatal(string(res.Body)) }
}
func TestImageContentIsRejected(t *testing.T) {
    e, _ := dlp.NewEngine(nil)
    _, err := e.Inspect([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`))
    if !errors.Is(err, dlp.ErrUnsupportedContent) { t.Fatalf("got %v", err) }
}
```

`internal/adminapi/dlp_test_endpoint_test.go`：

```go
func TestDLPTestEndpointReturnsMaskedText(t *testing.T) {
    env := newAdminEnv(t)
    env.loginAs("operator")
    rr := env.do("POST", "/admin/api/dlp-rules/test", `{"text":"call 13800138000"}`)
    if rr.Code != 200 { t.Fatalf("got %d %s", rr.Code, rr.Body) }
    if strings.Contains(rr.Body.String(), "13800138000") || !strings.Contains(rr.Body.String(), "***") { t.Fatal(rr.Body.String()) }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/dlp -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- 编译规则：`regexp.Compile`；关键词转小写；按 priority 排序；保留原始 `store.DLPRule` 供 Hit 使用。
- 内置规则：
  - 身份证：18 位正则 + 校验位；动作 `mask`。
  - 手机号：`1[3-9]\d{9}` 且前后非数字；`mask`。
  - 银行卡：16–19 位数字 + Luhn；`mask`。
  - 内网 IP：RFC1918 + loopback；`audit`。
  - 云 AK/SK：`AKIA[0-9A-Z]{16}`、`LTAI[0-9A-Za-z]{12,}`、`AKID[0-9A-Za-z]{13,}`；`block`。
  - 通用密钥：`sk-[A-Za-z0-9]{20,}`、`-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----`；`block`。
  - 邮箱：`audit`。
- JSON 遍历：递归处理 `map[string]any`、`[]any`、`string`；跳过 key 集合：`model`、`role`、`type`、`id`、`stream`、`max_tokens`、`max_completion_tokens`、`temperature`、`top_p`、`stop`。`tools`、`tool_choice` 中的字符串仍要扫描，仅跳过 `type` 等控制键。
- 检测 `type=image_url`、`type=image`、`type=document`、`type=input_audio`、`type=file` 或 key `source.data`/`file_data` → `ErrUnsupportedContent`。
- 非 JSON：按纯文本扫描；Mask 返回替换后的原始字节。
- 同一规则多处命中、不同规则重叠：收集区间，Block 优先；Mask 区间合并后从后往前替换。
- 内置规则通过 `EnsureBuiltinRules` 在启动时插入；`BuiltinRules` 的 name 必须与迁移/种子一致。
- `POST /admin/api/dlp-rules/test`：加载已启用规则（或请求体传入的临时规则），调用 `Engine.Test`，返回 `{hits, masked_text, action}`；不写审计、不写数据库；仅 `super_admin`/`operator`。
- 规则 CRUD 成功后调用 `Engine.Reload`，保证无需重启生效。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/dlp ./internal/adminapi -run 'TestDLP|TestBlock|TestMask|TestImage' -v`
Expected: PASS，包含 Luhn 校验、身份证校验、重叠命中、无效正则跳过、二进制拒绝、测试端点脱敏。

- [ ] **Step 5: 提交**

```bash
git add internal/dlp internal/adminapi
git commit -m "feat: add DLP rule engine, builtins, and JSON masking"
```

---

### Task 14: `/v1` 请求编排

**Files:**
- Create: `internal/gateway/handler.go`, `internal/gateway/interfaces.go`, `internal/gateway/handler_test.go`, `internal/gateway/requestid.go`, `internal/audit/event.go`, `internal/alert/event.go`

**Interfaces:**
- Consumes: Task 6 额度/限流、Task 10 认证、Task 11/12 代理、Task 13 DLP；审计只依赖本任务定义的 `audit.Event` 和 `Audit` 接口，Task 15 提供具体 Writer。
- Produces:
  - `gateway.New(HandlerDeps) *Handler`；`(*Handler).Handle(c *gin.Context)`
  - `audit.Event` 字段与设计文档 §8.1 一致；至少包含 `RequestID`、`EventTime`、`TokenID`、`EmployeeID`、`DepartmentID`、`ClientIP`、`Method`、`Path`、`Model`、`ProviderID`、`StatusCode`、`ErrorCode`、`LatencyMS`、`UpstreamLatencyMS`、`FirstByteMS`、`PromptTokens`、`CompletionTokens`、`TotalTokens`、`UsageEstimated`、`Prompt`、`Response`、`PromptTruncated`、`ResponseTruncated`、`DLPAction`、`DLPHits`、`ClientDisconnected`、`UpstreamRequestID`
  - `alert.Alert{RuleName, Employee, Action, RequestID string; At time.Time}`；邮件告警不包含 Prompt/Response。
  - `HandlerDeps` 包含接口：
    - `Authenticator interface { Authenticate(*http.Request) (*auth.Principal, error) }`
    - `Limiter interface { Allow(context.Context, string, int) (bool, time.Duration, error) }`
    - `Quota interface { Reserve(context.Context, *store.Token, quota.Endpoint, int64) (store.Reservation, error); Settle(context.Context, store.Reservation, int64) error }`
    - `DLP interface { Inspect([]byte) (dlp.Result, error) }`
    - `Resolver interface { Resolve(path, model string) (proxy.Target, error) }`
    - `Upstream interface { Do(context.Context, http.ResponseWriter, proxy.Request) (proxy.Result, error); Stream(context.Context, http.ResponseWriter, func(), proxy.Request) (proxy.Result, error) }`
    - `Audit interface { Enqueue(audit.Event) bool }`
    - `AlertSink interface { Enqueue(context.Context, alert.Alert) bool }`
  - 请求头 `X-Request-Id`：客户端未提供时生成 UUID；客户端值只接受 `[A-Za-z0-9-]{1,64}`，否则重新生成；写入响应头。

- [ ] **Step 1: 写失败测试**

```go
func TestPipelineBlockReturns403AndDoesNotCallUpstream(t *testing.T) {
    h, f := newHandlerFixture(t)
    f.dlpResult = dlp.Result{Action: "block", Hits: []dlp.Hit{{RuleName: "aws", Action: "block"}}}
    rr := httptest.NewRecorder()
    c, _ := gin.CreateTestContext(rr)
    c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"AKIA..."}]}`))
    c.Request.Header.Set("Authorization", "Bearer sk-company-test")
    h.Handle(c)
    if rr.Code != 403 { t.Fatalf("got %d", rr.Code) }
    if f.upstreamCalls != 0 { t.Fatal("upstream called") }
    if f.auditEvents == 0 { t.Fatal("no audit event") }
}
func TestPipelineMasksBeforeForwarding(t *testing.T) {
    h, f := newHandlerFixture(t)
    f.dlpResult = dlp.Result{Action: "mask", Body: []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"***"}]}`)}
    // 调用后断言 f.lastUpstreamBody 包含 "***" 且不包含原手机号
}
func TestQuotaExceededReturns402(t *testing.T) {
    h, f := newHandlerFixture(t)
    f.quotaErr = store.ErrQuotaExceeded
    // 断言 402，upstream 未被调用
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/gateway -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

顺序严格为：
1. `X-Request-Id`。
2. 解析并限制请求体（`http.MaxBytesReader`，2 MiB）；超限 413。
3. 认证；错误映射 401。
4. 限流；超限 429 + `Retry-After`。
5. 解析 JSON 的 `model`；路径协议不支持返回 501。
6. Resolver 选上游；失败 400。
7. DLP `Inspect`；`ErrUnsupportedContent` → 415；`block` → 403；`mask` 用返回的 Body；`audit` 继续；空 Action 视为 `none`。
8. 判断端点：`GET /v1/models` 和 `POST /v1/messages/count_tokens` 是元数据端点，跳过配额；`/v1/embeddings` 用嵌入规则；其余生成端点按 Task 6 预留；`ErrQuotaExceeded` → 402。
9. 调用 `Proxy.Do` 或 `Proxy.Stream`；流式使用 `c.Writer.Flush`。
10. 结算额度：生成按实际 usage；缺失用预留并标记估算；嵌入按实际；请求额度 actual=1。
11. 构造 `audit.Event`：`Prompt` 保存原始请求（Mask 前），便于事后调查；上游实际收到的脱敏 body 不单独保存。`Response` 保存上游返回内容；两者由 writer 加密。`Enqueue` 绝不阻塞。
12. DLP 命中且规则 `alert_enabled` 为 true（Block 或 Audit）时，向 `AlertSink` 投递 `alert.Alert`；不包含 Prompt/Response。
13. 上游错误：502/504；保留审计。
14. 客户端断连：取消并记录。
15. 认证/额度存储本身不可用（例如 PostgreSQL 连接失败）→ 503，不能误判为 401/402；仅明确的凭证/额度错误才返回 401/402。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/gateway -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/gateway internal/audit/event.go internal/alert/event.go
git commit -m "feat: orchestrate gateway auth, quota, DLP, proxy, and audit"
```

---

### Task 15: 异步审计 Writer、加密与 spool

**Files:**
- Create: `internal/audit/writer.go`, `internal/audit/spool.go`, `internal/audit/retention.go`, `internal/audit/writer_test.go`, `internal/audit/spool_test.go`, `internal/audit/retention_test.go`

**Interfaces:**
- Consumes: Task 2 `store.AuditStore`、Task 3 `Cipher`、Task 14 `audit.Event`。
- Produces:
  - `audit.NewWriter(cfg WriterConfig) *Writer`；`WriterConfig{Store AuditBatchStore; Cipher *crypto.Cipher; QueueSize int; BatchSize int; FlushInterval time.Duration; SpoolDir string; Logger *slog.Logger}`
  - `audit.NewRetention(store AuditRetentionStore, retentionDays int, clock func() time.Time) *Retention`
  - `(*Retention).Run(ctx context.Context)`、`(*Retention).RunOnce(ctx context.Context) (int64, error)`；启动时执行一次，之后每天 03:00（Asia/Shanghai）执行；`AuditRetentionStore` 接口为 `DeleteBefore(ctx context.Context, before time.Time) (int64, error)`。
  - `(*Writer).Start(ctx context.Context)`；`(*Writer).Enqueue(e Event) bool`；`(*Writer).Close(ctx context.Context) error`
  - `(*Writer).Dropped() int64`、`(*Writer).Spooled() int64`
  - `spool.Write(dir string, cipher *crypto.Cipher, record *store.AuditRecord) error`
  - `spool.Replay(ctx context.Context, dir string, cipher *crypto.Cipher, sink AuditBatchStore) (int, error)`；spool 文件 0600，按 10 MiB 轮转。
  - `AuditBatchStore` 接口：`InsertBatch(ctx context.Context, records []*store.AuditRecord) error`

- [ ] **Step 1: 写失败测试**

```go
func TestEnqueueNeverBlocksWhenQueueFull(t *testing.T) {
    w := audit.NewWriter(audit.WriterConfig{Store: &blockingStore{}, Cipher: testCipher(t), QueueSize: 1, BatchSize: 1, FlushInterval: time.Hour, SpoolDir: t.TempDir()})
    w.Start(ctx)
    w.Enqueue(event("1"))
    done := make(chan struct{})
    go func() { w.Enqueue(event("2")); close(done) }()
    select { case <-done: case <-time.After(100 * time.Millisecond): t.Fatal("Enqueue blocked") }
}
func TestWriterEncryptsPromptAndResponse(t *testing.T) {
    store := &captureStore{}
    w := audit.NewWriter(...); w.Start(ctx); w.Enqueue(event("1")); w.Close(ctx)
    if bytes.Contains(store.records[0].PromptEnc, []byte("secret")) { t.Fatal("prompt not encrypted") }
    plain, err := testCipher(t).Open(store.records[0].PromptEnc, "req-1:prompt")
    if err != nil || string(plain) != "secret" { t.Fatalf("%q %v", plain, err) }
}
func TestSpoolReplay(t *testing.T) {
    dir := t.TempDir()
    spool.Write(dir, testCipher(t), record("req-1"))
    n, err := spool.Replay(ctx, dir, testCipher(t), &captureStore{})
    if err != nil || n != 1 { t.Fatalf("n=%d err=%v", n, err) }
}
func TestRetentionDeletesOnlyExpired(t *testing.T) {
    store := &retentionFake{deletedBefore: time.Time{}}
    r := audit.NewRetention(store, 180, func() time.Time { return time.Date(2026, 11, 2, 3, 0, 0, 0, shanghai) })
    r.RunOnce(ctx)
    want := time.Date(2026, 5, 6, 3, 0, 0, 0, shanghai) // 180 days before
    if !store.deletedBefore.Equal(want) { t.Fatalf("got %v", store.deletedBefore) }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/audit -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- Writer 启动一个 goroutine，从 channel 读取，按 `BatchSize` 或 `FlushInterval` 批量；每次写入把 Event 转为 `store.AuditRecord`：
  - `PromptEnc = Cipher.Seal(Prompt, RequestID+":prompt")`
  - `ResponseEnc = Cipher.Seal(Response, RequestID+":response")`
  - `DLPHits` 编码为 JSONB 字节。
- `Enqueue` 非阻塞：channel 满时写 spool 并返回 false；spool 失败计数并记录错误。
- spool 只存密文：把 `store.AuditRecord` JSON 序列化后，用 `Cipher.Seal(payload, "spool:"+record.RequestID)` 加密，写成一行 base64；回放时用同一 AAD 解密。文件权限 0600，按 10 MiB 轮转。
- DB 写失败：重试 3 次（100ms/500ms/2s），仍失败则逐条 spool。
- 启动时先 `Replay`，之后每 30 秒回放一次。
- `Close`：停止接收，关闭 channel，等待排空；超时返回错误。
- 截断：写入前若 `len(Prompt) > 256 KiB`，截断并置 `PromptTruncated`；Response 同理。
- 保留任务：`Retention.Run` 启动时执行一次，之后按 Asia/Shanghai 每天 03:00 调用 `DeleteBefore(now - retentionDays)`；删除失败只记录日志并次日重试。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/audit -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/audit
git commit -m "feat: add non-blocking encrypted audit writer with spool"
```

---

### Task 16: 邮件告警与 SMTP 设置

**Files:**
- Create: `internal/alert/mailer.go`, `internal/alert/settings.go`, `internal/alert/mailer_test.go`, `internal/alert/settings_test.go`

**Interfaces:**
- Consumes: Task 14 `alert.Alert`、Task 2 `SettingsStore`、Task 3 `Cipher`。
- Produces:
  - `alert.SMTPConfig{Host string; Port int; Username, Password, From string; To []string; UseTLS bool}`
  - `alert.NewMailer(cfg SMTPConfig) Mailer`；`Mailer interface { Send(ctx context.Context, subject, body string) error }`
  - `alert.NewWorker(loader func(context.Context) (Mailer, error), queueSize int, dedupWindow time.Duration, clock func() time.Time) *Worker`
  - `(*Worker).Enqueue(ctx context.Context, e Alert) bool`；`(*Worker).Start(ctx context.Context)`；`(*Worker).Close(ctx context.Context) error`
  - `alert.LoadSMTP(ctx, settings store.SettingsStore, cipher *crypto.Cipher) (SMTPConfig, error)`；密码字段 `smtp.password_enc` 解密。
  - 去重键：`ruleName + ":" + employee`，窗口 5 分钟。
  - 邮件正文不包含 Prompt/Response。

- [ ] **Step 1: 写失败测试**

```go
func TestAlertDedup(t *testing.T) {
    m := &fakeMailer{}
    w := alert.NewWorker(func(context.Context) (alert.Mailer, error) { return m, nil }, 10, 5*time.Minute, func() time.Time { return now })
    w.Start(ctx)
    w.Enqueue(ctx, alert.Alert{RuleName: "aws", Employee: "alice", Action: "block", RequestID: "1", At: now})
    w.Enqueue(ctx, alert.Alert{RuleName: "aws", Employee: "alice", Action: "block", RequestID: "2", At: now.Add(time.Minute)})
    w.Close(ctx)
    if m.count != 1 { t.Fatalf("sent %d", m.count) }
}
func TestAlertBodyHasNoPrompt(t *testing.T) {
    // 构造含 Prompt 的 Alert 不可能；断言邮件正文只包含 rule/employee/request_id
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/alert -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- 使用 `github.com/wneessen/go-mail` 发送；支持 STARTTLS；超时 10 秒。
- Worker 有界 channel；发送失败重试 3 次（1s/5s/30s），失败只记录日志。
- 去重 map + mutex，按窗口清理。
- 设置读取：`settings` 表 key `smtp`，JSON 字段；密码用 `Cipher.Seal` 加密，AAD `setting:smtp.password`。第一版在每次发送告警前调用 `LoadSMTP`，让后台修改立即生效；不要缓存解密的 SMTP 密码超过单次发送。
- 邮件主题：`[DLP] <action> - <rule>`；正文包含时间、员工、规则、request_id，不含敏感原文。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/alert -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/alert
git commit -m "feat: add SMTP alert worker with deduplication"
```

---

### Task 17: 审计查询、原文查看与 Dashboard API

**Files:**
- Create: `internal/adminapi/audit.go`, `internal/adminapi/dashboard.go`, `internal/adminapi/audit_test.go`, `internal/adminapi/dashboard_test.go`

**Interfaces:**
- Consumes: Task 2 `AuditStore`、Task 3 `Cipher`、Task 7 中间件、Task 15 `store.AuditRecord`。
- Produces:
  - `GET /admin/api/audit?from=&to=&employee_id=&status_code=&dlp_action=&model=&cursor=&limit=`
  - `GET /admin/api/audit/:id`：元数据，不含原文。
  - `POST /admin/api/audit/:id/reveal`：body `{"reason":"..."}`；仅 `super_admin`；返回 `{prompt, response}` 明文；写 `admin_audit_logs`。
  - `GET /admin/api/dashboard/overview?from=&to=`
  - `GET /admin/api/dashboard/trend?from=&to=&interval=day`
  - `GET /admin/api/dashboard/top-users?from=&to=&limit=10`
  - `GET /admin/api/dashboard/dlp?from=&to=`
  - 列表分页使用 `cursor`（`event_time,id` 的 base64 编码）和 `limit`，最大 200。

- [ ] **Step 1: 写失败测试**

```go
func TestRevealRequiresSuperAdminAndReason(t *testing.T) {
    env := newAdminEnv(t)
    env.loginAs("auditor")
    rr := env.do("POST", "/admin/api/audit/1/reveal", `{"reason":"investigation"}`)
    if rr.Code != 403 { t.Fatalf("auditor got %d", rr.Code) }
    env.loginAs("super_admin")
    rr = env.do("POST", "/admin/api/audit/1/reveal", `{"reason":""}`)
    if rr.Code != 400 { t.Fatalf("empty reason got %d", rr.Code) }
}
func TestRevealWritesAdminAudit(t *testing.T) {
    env := newAdminEnv(t)
    env.loginAs("super_admin")
    rr := env.do("POST", "/admin/api/audit/1/reveal", `{"reason":"customer complaint"}`)
    if rr.Code != 200 { t.Fatalf("got %d", rr.Code) }
    if env.adminAuditCount("reveal_audit") != 1 { t.Fatal("missing admin audit") }
}
func TestAuditListNeverReturnsPlaintext(t *testing.T) {
    // 列表响应不得包含 "secret prompt"；详情也不得包含
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/adminapi -run 'TestReveal|TestAuditList' -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- 列表：动态 WHERE 参数化；时间范围默认近 24 小时；keyset 分页；返回 `dlp_hits` 的规则名/动作，不返回原文。
- 详情：同样不含密文/明文。
- Reveal：校验 `reason` 长度 ≥ 5；用 `Cipher.Open(record.PromptEnc, record.RequestID+":prompt")` 和 response AAD；写入 `admin_audit_logs`（action=`reveal_audit`，detail 含 reason、fields）；响应 `Cache-Control: no-store`。
- Dashboard：单条 SQL 聚合；返回今日/区间总量、活跃员工、DLP 命中、错误率；趋势按天；Top users；DLP 规则排行。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/adminapi -run 'TestReveal|TestAudit|TestDashboard' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/adminapi
git commit -m "feat: add audit search, reveal, and dashboard APIs"
```

---

### Task 18: 设置与管理员账号 API

**Files:**
- Create: `internal/adminapi/settings.go`, `internal/adminapi/admin_users.go`, `internal/adminapi/settings_test.go`, `internal/adminapi/admin_users_test.go`, `internal/store/postgres/settings.go`, `internal/store/postgres/settings_test.go`

**Interfaces:**
- Consumes: Task 2 `SettingsStore`/`AdminStore`、Task 3 `Cipher`/password、Task 7 中间件、Task 16 `LoadSMTP`。
- Produces: `*postgres.Store` 实现 `SettingsStore`（`settings` 表按 key upsert、按 key 读取；缺失返回 `store.ErrNotFound`）。
- Produces:
  - `GET/PUT /admin/api/settings/smtp`；`POST /admin/api/settings/smtp/test`
  - `GET/PUT /admin/api/settings/audit`（保留天数、告警开关）
  - `GET/POST /admin/api/admin-users`；`PUT /admin/api/admin-users/:id`；`POST /admin/api/admin-users/:id/reset-password`
  - 仅 `super_admin` 可访问；密码只写不读。

- [ ] **Step 1: 写失败测试**

```go
func TestSMTPPasswordNeverReturned(t *testing.T) {
    env := newAdminEnv(t); env.loginAs("super_admin")
    env.do("PUT", "/admin/api/settings/smtp", `{"host":"smtp.example.com","port":587,"username":"u","password":"secret","from":"a@example.com","to":["b@example.com"],"use_tls":true}`)
    rr := env.do("GET", "/admin/api/settings/smtp", "")
    if strings.Contains(rr.Body.String(), "secret") { t.Fatal("leaked smtp password") }
}
func TestOnlySuperAdminCanManageAdmins(t *testing.T) {
    env := newAdminEnv(t); env.loginAs("operator")
    if rr := env.do("GET", "/admin/api/admin-users", ""); rr.Code != 403 { t.Fatalf("got %d", rr.Code) }
}
func TestSettingsStoreUpsert(t *testing.T) {
    s := newTestStore(t)
    if err := s.Set(ctx, "audit", []byte(`{"retention_days":180}`)); err != nil { t.Fatal(err) }
    if err := s.Set(ctx, "audit", []byte(`{"retention_days":90}`)); err != nil { t.Fatal(err) }
    got, err := s.Get(ctx, "audit")
    if err != nil || !bytes.Contains(got, []byte("90")) { t.Fatalf("got=%s err=%v", got, err) }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/adminapi ./internal/store/postgres -run 'TestSMTP|TestOnlySuperAdmin|TestSettingsStore' -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- `postgres/settings.go`：`Get` 查询 `settings` 表，`Set` 使用 `INSERT ... ON CONFLICT (key) DO UPDATE SET value=$2, updated_at=now()`。
- SMTP 设置读写；`settings` 的 JSON 保存 `password_enc`（base64 的 AES-GCM 信封），绝不保存 `password` 字段；读取返回 `password_set: true`，不返回密码；更新时密码为空表示保留原密码。AAD 固定 `setting:smtp.password`。
- 测试邮件调用 `Mailer.Send`，失败返回 502 和摘要。
- 管理员 CRUD：用户名唯一；角色枚举；禁用；重置密码使用 Argon2id；不能禁用最后一个 `super_admin`。
- 所有写操作写 `admin_audit_logs`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/adminapi -run 'TestSMTP|TestAdminUser' -v` 和 `TEST_DATABASE_URL=... go test ./internal/store/postgres -run TestSettingsStore -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/adminapi internal/store/postgres
git commit -m "feat: add settings and admin user management APIs"
```

---

### Task 19: 管理后台脚手架、登录与布局

**Files:**
- Create: `web/package.json`, `web/tsconfig.json`, `web/vite.config.ts`, `web/index.html`, `web/src/main.ts`, `web/src/App.vue`, `web/src/router/index.ts`, `web/src/api/client.ts`, `web/src/api/types.ts`, `web/src/stores/auth.ts`, `web/src/layouts/AdminLayout.vue`, `web/src/views/LoginView.vue`, `web/src/views/DashboardView.vue`（占位）, `web/src/views/LoginView.spec.ts`, `web/src/api/client.spec.ts`
- Modify: `internal/server/server.go`（生产构建后由 Caddy 托管，Go 服务不嵌入静态文件）

**Interfaces:**
- Consumes: Task 7/8/9 的 `/admin/api` 契约。
- Produces: Vue 3 + TS 应用；`apiClient` 封装 `credentials: "include"` 和 `X-CSRF-Token`（从 `csrf_token` Cookie 读取）；路由守卫在未登录时跳转 `/admin/login`。

- [ ] **Step 1: 初始化前端并写失败测试**

```bash
cd D:/work/api-key/web
npm create vite@latest . -- --template vue-ts
npm install
npm install element-plus @element-plus/icons-vue vue-router pinia echarts
npm install -D vitest @vue/test-utils jsdom
```

`web/package.json` 的 scripts 固定为 `dev`、`build`（`vue-tsc -b && vite build`）、`test`（`vitest run`）。

`web/src/api/client.spec.ts`：

```ts
it("adds csrf header for mutations", async () => {
  document.cookie = "csrf_token=abc";
  const fetchMock = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
  await apiClient.post("/admin/api/tokens", {});
  const init = fetchMock.mock.calls[0][1];
  expect(init.headers["X-CSRF-Token"]).toBe("abc");
  expect(init.credentials).toBe("include");
});
```

`web/src/views/LoginView.spec.ts`：

```ts
it("shows the server error on failed login", async () => {
  vi.spyOn(authStore, "login").mockRejectedValue(new Error("invalid credentials"));
  const wrapper = mount(LoginView, { global: { plugins: [pinia] } });
  await wrapper.find("[data-test=username]").setValue("admin");
  await wrapper.find("[data-test=password]").setValue("bad");
  await wrapper.find("form").trigger("submit");
  await flushPromises();
  expect(wrapper.text()).toContain("invalid credentials");
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/api/client.spec.ts`
Expected: FAIL，`apiClient` 未定义。

- [ ] **Step 3: 实现脚手架**

- `vite.config.ts` 配置 `/admin/api` 代理到 `http://localhost:8080`；`test.environment = "jsdom"`；生产由 Caddy 同源。
- `apiClient`：`get/post/put/del`，解析统一响应包，错误抛出 `ApiError`；401 清空 auth 并跳转登录。
- `auth` store：`me()`、`login()`、`logout()`、`isAuthenticated`。
- `AdminLayout`：侧边栏菜单（概览、员工、Token、上游、模型路由、DLP、审计、设置、管理员），顶部用户/退出。
- `LoginView`：用户名密码、错误提示。
- 路由守卫：除 `/admin/login`（管理端）与 `/portal/login`（员工端）外要求登录。
- 为尚未实现的菜单页创建最小占位 view（只渲染标题），保证本任务结束时可构建；后续任务替换。
- Dashboard 占位以待 Task 22。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npx vitest run src/api/client.spec.ts src/views/LoginView.spec.ts`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add web
git commit -m "feat: scaffold Vue admin with auth and layout"
```

---

### Task 20: 员工与 Token 管理页面

**Files:**
- Create: `web/src/views/EmployeesView.vue`, `web/src/views/TokensView.vue`, `web/src/components/TokenCreateDialog.vue`, `web/src/views/EmployeesView.spec.ts`, `web/src/components/TokenCreateDialog.spec.ts`
- Modify: `web/src/router/index.ts`

**Interfaces:**
- Consumes: Task 8 API。
- Produces: 员工 CRUD 页面；Token 列表/创建/编辑/撤销/轮换；创建成功只展示一次完整 Key。

- [ ] **Step 1: 写失败测试**

```ts
it("shows full token once and hides it after confirm", async () => {
  const wrapper = mount(TokenCreateDialog, { props: { modelValue: true, token: "sk-company-abc" } });
  expect(wrapper.text()).toContain("sk-company-abc");
  await wrapper.find("[data-test=confirm-saved]").trigger("click");
  expect(wrapper.emitted("update:modelValue")?.[0]).toEqual([false]);
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/components/TokenCreateDialog.spec.ts`
Expected: FAIL。

- [ ] **Step 3: 实现**

- 员工表格：搜索、部门筛选、新增/编辑、状态。
- Token 表格：员工、前缀、额度类型/周期/总量/已用、RPM、状态、过期时间。
- 创建弹窗：员工、额度、周期、过期时间、IP 白名单（多行）、RPM；成功后显示完整 Key + 复制按钮 + “我已保存”关闭。
- 撤销/轮换二次确认；轮换后展示新 Key。
- 所有日期按 `Asia/Shanghai` 显示。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npx vitest run src/views/EmployeesView.spec.ts src/components/TokenCreateDialog.spec.ts`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add web
git commit -m "feat: add employee and token admin pages"
```

---

### Task 21: Provider、模型路由与 DLP 规则页面

**Files:**
- Create: `web/src/views/ProvidersView.vue`, `web/src/views/ModelRoutesView.vue`, `web/src/views/DLPRulesView.vue`, `web/src/views/DLPRulesView.spec.ts`
- Modify: `web/src/router/index.ts`

**Interfaces:**
- Consumes: Task 9 API。
- Produces: Provider 列表/表单/连通性测试；模型路由 CRUD；DLP 规则 CRUD + 测试面板 + 关键词导入。

- [ ] **Step 1: 写失败测试**

```ts
it("renders masked DLP test result without leaking original sensitive value", async () => {
  const wrapper = mount(DLPRulesView, { global: { stubs: { ... } } });
  await wrapper.find("[data-test=dlp-sample]").setValue("13800138000");
  await wrapper.find("[data-test=dlp-test]").trigger("click");
  expect(wrapper.text()).toContain("***");
  expect(wrapper.text()).not.toContain("13800138000");
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/views/DLPRulesView.spec.ts`
Expected: FAIL。

- [ ] **Step 3: 实现**

- Provider 表单：名称、协议、Base URL、API Key（只写，编辑时留空表示不修改）、默认、超时；测试按钮展示可达/失败。
- 模型路由：协议、匹配类型、pattern、Provider、上游模型、优先级、启用；保存前校验协议一致。
- DLP：内置/自定义筛选；规则表单；关键词批量导入（每行一个）；测试面板显示命中规则、动作、脱敏后文本。
- 所有删除/禁用二次确认。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npx vitest run src/views/DLPRulesView.spec.ts`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add web
git commit -m "feat: add provider, model route, and DLP rule pages"
```

---

### Task 22: Dashboard、审计与设置页面

**Files:**
- Create: `web/src/views/DashboardView.vue`, `web/src/views/AuditView.vue`, `web/src/views/SettingsView.vue`, `web/src/views/AdminUsersView.vue`, `web/src/views/AuditView.spec.ts`
- Modify: `web/src/router/index.ts`, `web/src/layouts/AdminLayout.vue`

**Interfaces:**
- Consumes: Task 17/18 API。
- Produces: 概览卡片/趋势图/Top 排行；审计列表/详情/原文查看；SMTP 设置/测试；管理员管理。

- [ ] **Step 1: 写失败测试**

```ts
it("requires a reason before revealing audit content", async () => {
  const wrapper = mount(AuditView, { global: { stubs: { ... } } });
  await wrapper.find("[data-test=reveal]").trigger("click");
  expect(wrapper.find("[data-test=reveal-confirm]").attributes("disabled")).toBeDefined();
  await wrapper.find("[data-test=reveal-reason]").setValue("customer complaint");
  expect(wrapper.find("[data-test=reveal-confirm]").attributes("disabled")).toBeUndefined();
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/views/AuditView.spec.ts`
Expected: FAIL。

- [ ] **Step 3: 实现**

- Dashboard：ECharts 折线（调用趋势）、卡片（调用量/Token/活跃员工/DLP 命中）、Top 员工/规则。
- 审计：时间/员工/状态码/DLP 动作/模型筛选；表格；详情抽屉；原文默认隐藏，超级管理员输入原因后显示；查看后不缓存。
- 设置：SMTP 表单（密码留空不修改）、测试邮件、审计保留天数、告警开关。
- 管理员：列表、创建、角色、禁用、重置密码；仅超级管理员可见。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npx vitest run src/views/AuditView.spec.ts && npm run build`
Expected: PASS，构建成功。

- [ ] **Step 5: 提交**

```bash
git add web
git commit -m "feat: add dashboard, audit, and settings pages"
```

---

### Task 23: 部署产物、Caddy、备份与运维文档

**Files:**
- Create: `deploy/gateway.Dockerfile`, `deploy/web.Dockerfile`, `deploy/Caddyfile`, `deploy/backup.sh`, `docker-compose.yml`, `.env.production.example`, `docs/operations/deploy.md`, `docs/operations/restore.md`, `docs/operations/employee-onboarding.md`
- Modify: `Makefile`

**Interfaces:**
- Consumes: 所有前面任务的二进制/前端构建。
- Produces: 可运行的 Compose 栈；Caddy 自动 HTTPS；Postgres/Redis 内网；备份脚本。

- [ ] **Step 1: 写部署验证脚本/测试**

`Makefile` 增加：

```make
compose-config:
	docker compose -f docker-compose.yml config >/dev/null

compose-build:
	docker compose -f docker-compose.yml build
```

- [ ] **Step 2: 运行验证确认失败**

Run: `make compose-config`
Expected: FAIL，`docker-compose.yml` 不存在。

- [ ] **Step 3: 实现**

- `gateway.Dockerfile`：多阶段 `golang:1.23-alpine` 构建，`distroless` 或 `alpine` 运行；非 root；复制迁移；暴露 8080。
- `web.Dockerfile`：`node:24-alpine` 构建 `web/dist`，复制到 Caddy 镜像。
- `docker-compose.yml`：`caddy`、`gateway`、`postgres`、`redis`；健康检查；卷；`env_file`；`depends_on`；只暴露 80/443。
- `Caddyfile`：
  - `{$DOMAIN}` 自动 HTTPS。
  - `/v1/*` 与 `/admin/api/*` 反代 `gateway:8080`；`flush_interval -1`。
  - 其他路径静态 `web/dist`，SPA fallback `try_files {path} /index.html`。
  - `/metrics` 默认不对外路由；需要采集时只允许内网来源或走独立监听端口。
  - 安全头：HSTS、`X-Content-Type-Options`、`Referrer-Policy`。
- `backup.sh`：`pg_dump -Fc`，使用 `BACKUP_PASSPHRASE` 做 AES-256 加密（不得与 `MASTER_KEY` 相同），保留 7 天；可选 `rclone copy` 到 COS/S3；失败非零退出。
- `docs/operations/employee-onboarding.md`：OpenAI SDK、Anthropic SDK 配置样例；说明 `base_url`、`api_key`/`x-api-key`、流式用法。
- `docs/operations/deploy.md`：域名、DNS、证书、防火墙、bootstrap 管理员、迁移、健康检查、日志、监控、回滚；创建两个数据库角色：应用角色对 `audit_logs` 只有 INSERT/SELECT，维护角色只用于保留任务 DELETE，并配置 `AUDIT_MAINTENANCE_DATABASE_URL`；写明旧上游 Key 撤销步骤、员工通知和 1–2 周观察期。
- `docs/operations/restore.md`：恢复步骤和演练命令。

- [ ] **Step 4: 运行验证确认通过**

Run: `make compose-config && make compose-build`
Expected: 配置校验和镜像构建成功。

- [ ] **Step 5: 提交**

```bash
git add deploy docker-compose.yml .env.production.example docs/operations Makefile
git commit -m "feat: add production compose, Caddy, backups, and runbooks"
```

---

### Task 24: 指标与可观测性

**Files:**
- Create: `internal/metrics/metrics.go`, `internal/metrics/metrics_test.go`
- Modify: `internal/server/server.go`、`internal/gateway/handler.go`、`internal/audit/writer.go`

**Interfaces:**
- Produces:
  - `metrics.New() *Registry`
  - `(*Registry).Handler() http.Handler`
  - `(*Registry).IncRequest(status int, dlpAction string)`
  - `(*Registry).ObserveLatency(ms float64)`
  - `(*Registry).IncDLPHit(rule, action string)`
  - `(*Registry).SetAuditQueueDepth(n int)`
  - `(*Registry).SetSpoolFiles(n int)`
  - `GET /metrics` 返回 Prometheus 文本格式。
- 标签禁止包含员工、Token、request_id、路径等高基数维度。

- [ ] **Step 1: 写失败测试**

```go
func TestMetricsEndpointExposesRequestCounter(t *testing.T) {
    r := metrics.New()
    r.IncRequest(200, "none")
    rr := httptest.NewRecorder()
    r.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
    if rr.Code != 200 || !strings.Contains(rr.Body.String(), "gateway_requests_total") {
        t.Fatalf("got %d %s", rr.Code, rr.Body.String())
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/metrics -v`
Expected: FAIL。

- [ ] **Step 3: 实现并接入**

```bash
go get github.com/prometheus/client_golang@v1.20.5
```

- 定义 CounterVec `gateway_requests_total{status,dlp_action}`、Histogram `gateway_request_duration_ms`、CounterVec `gateway_dlp_hits_total{rule,action}`、Gauge `audit_queue_depth`、Gauge `audit_spool_files`。
- `server.New` 挂载 `/metrics`（无需登录，只在内网/Caddy 限制访问）。
- Gateway handler 在返回前 `IncRequest`、`ObserveLatency`；DLP 命中时 `IncDLPHit`。
- Audit writer 每次 flush/入队后更新队列深度与 spool 文件数。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/metrics ./internal/gateway ./internal/audit -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/metrics internal/server internal/gateway internal/audit go.mod go.sum
git commit -m "feat: add Prometheus metrics"
```

---

### Task 25: 端到端、并发与安全验收

**Files:**
- Create: `internal/e2e/gateway_test.go`, `internal/e2e/load_test.go`, `docs/operations/live-verification.md`, `scripts/live_check.sh`
- Modify: `cmd/gateway/main.go`（最终依赖装配）、`internal/server/server.go`（挂载 `/v1` 和 `/admin/api`）

**Interfaces:**
- Consumes: 全部任务。
- Produces: 可运行的完整网关；E2E 覆盖流式/非流式、DLP 三动作、401/402/403/429/502/504；并发流式测试；真实上游验收清单。

- [ ] **Step 1: 写端到端失败测试**

```go
func TestEndToEndMaskBlockAndAudit(t *testing.T) {
    env := newE2E(t) // 测试 DB + 内存 Redis + mock upstream
    env.createToken(t, 10000)
    // 1) Mask: 手机号 -> 上游收到 ***，返回 200，审计 dlp_action=mask
    // 2) Block: AWS key -> 403，上游调用次数不增加
    // 3) Audit: 邮箱 -> 200，审计 dlp_action=audit
}
func TestEndToEndStreamingUsage(t *testing.T) {
    // mock upstream 返回 OpenAI SSE，usage total_tokens=11；断言客户端收到完整内容且额度扣减 11
}
func TestAuditDLPHitSendsOneEmail(t *testing.T) {
    // Audit 规则 alert_enabled=true；两次相同命中；fake mailer 只收到 1 封，正文不含 Prompt
}
func TestAuthenticatedButQuotaExhaustedReturns402(t *testing.T) {}
func TestClientDisconnectCancelsUpstream(t *testing.T) {
    // 客户端读完第一块后取消；断言上游 ctx.Done() 触发，gateway goroutine 退出
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `TEST_DATABASE_URL=... go test ./internal/e2e -v`
Expected: FAIL。

- [ ] **Step 3: 完成依赖装配与 E2E 实现**

- `cmd/gateway/main.go` 构造：pgx pool、Redis client、crypto、stores、quota、limiter fallback、auth、adminauth、DLP engine（启动时 `EnsureBuiltinRules` 并 Reload）、resolver、proxy、audit writer、audit retention worker（使用独立的 `AUDIT_MAINTENANCE_DATABASE_URL` 连接池，未配置则禁用）、alert worker、admin API、gateway handler。
- 路由：`/v1/*` 使用 gateway handler；`POST /admin/api/auth/login` 公开；`/admin/api/auth/logout|me` 需 Session，logout 另外要求 CSRF；其余 `/admin/api/*` 依次挂 `RequireSession`、写操作 `RequireCSRF`、按资源 `RequireRole`；`/healthz`、`/readyz`、`/metrics`。
- 启动时 Redis ping 失败只记录警告；Postgres ping 失败则退出。
- E2E 使用 `httptest.NewServer` 启动完整 server；mock upstream 验证 forwarded body/headers。
- 并发测试：100 个并发流式请求，每个 20 个 SSE chunk；断言无错误、无 goroutine 泄漏（测试前后 `runtime.NumGoroutine` 不持续增长）。
- 性能验证：mock upstream 立即返回时，网关附加延迟 p95 ≤ 10ms；DLP 对 10–50 KiB 典型请求 p95 ≤ 5ms。用 `testing.B` 或 load test 输出记录到 `docs/operations/live-verification.md`。
- 安全测试：超 2 MiB 返回 413；图片 content block 返回 415；不支持端点 501；无效 Token 401；跨协议路由 400/500 不转发。

- [ ] **Step 4: 运行全部测试确认通过**

Run:
```bash
go test ./... -count=1
cd web && npm run test && npm run build
```
Expected: 全部 PASS。

- [ ] **Step 5: 写真实上游验收清单**

`docs/operations/live-verification.md` 列出：
- OpenAI、Claude、DeepSeek 各一个聊天模型，流式/非流式各一次。
- 使用 ChatGPT/OpenAI SDK、Anthropic SDK、DeepSeek OpenAI 兼容 SDK 指向网关。
- 检查审计记录、usage、额度扣减、DLP Mask/Block/Audit。
- 旧 Key 撤销后，直连请求失败、网关请求成功。
- `scripts/live_check.sh` 读取 `GATEWAY_URL`、`COMPANY_TOKEN`、`OPENAI_MODEL`、`ANTHROPIC_MODEL`、`DEEPSEEK_MODEL`，发送最小请求并输出状态。

- [ ] **Step 6: 提交**

```bash
git add internal/e2e cmd internal/server docs/operations scripts
git commit -m "test: add end-to-end, streaming, and security verification"
```

---

## Self-Review Notes

- Spec coverage：认证、配额、限流、IP 白名单、三上游转发、SSE、DLP 三动作、异步审计、加密、邮件告警、Dashboard、RBAC、原文查看、Docker 部署、备份、旧 Key 切换均有对应任务。
- 明确不做：ClickHouse、协议转换、多模态、SSO、两人审批、响应 DLP 改写；这些与设计文档的非目标一致。
- 类型一致性：`store.Reservation`、`quota.Endpoint`、`proxy.Target`、`proxy.Result`、`audit.Event`、`dlp.Result` 在任务间保持一致。
- Review Focus 的测试归属：SSE → Task 12；客户端断连 → Task 12/25；Redis 降级 → Task 10/6；spool → Task 15；DLP Mask → Task 13/14。
