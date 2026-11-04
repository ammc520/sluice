# Sluice

Sluice（闸）——企业的统一大模型 API 网关。所有上游模型流量都经此放行、计量与留痕：员工用公司 Token 访问 OpenAI、Anthropic（Claude）、DeepSeek，网关负责认证、员工共享额度、速率限制、DLP 脱敏/阻断、异步审计与上游密钥托管。

## 能力

- **统一入口**：`/v1/chat/completions`、`/v1/messages` 等端点兼容 OpenAI / Anthropic 协议，客户端只改 Base URL。
- **凭据托管**：上游 Key 使用 AES-256-GCM 加密存储，任何接口都不回显明文。
- **员工额度**：按员工共享配额池、Token 生命周期（签发/轮换/吊销）与自助门户。
- **DLP**：手机号、身份证、邮箱、云厂商 AK 等内置规则，支持 mask / block / audit 三种动作。
- **审计**：请求与响应加密留存、超级管理员凭理由查看原文、管理员操作日志。
- **可运维**：Redis 限流降级到 PostgreSQL + 内存、审计 spool、Prometheus 指标、健康检查。

## 目录

```
cmd/sluice/        进程入口
cmd/migrate/       数据库迁移
internal/          网关、管理 API、门户 API、DLP、审计、配额等
web/               Vue 3 管理后台与员工门户
deploy/            Dockerfile、Caddyfile、备份脚本
docs/              设计与运维文档
```

## 本地启动

```bash
make dev-up                 # PostgreSQL + Redis
set -a; source .env; set +a # 写入 MASTER_KEY 等配置
make run                    # 默认监听 :8080
```

管理后台：`http://localhost:8080/admin/login`；员工门户：`/portal/login`；网关：`/v1`。

## 测试

```bash
make test          # 单元测试
make web-test      # 前端测试
make test-db       # 需要本地 PostgreSQL
```

## 文档

- [设计文档](docs/design.md)
- [实施计划](docs/implementation-plan.md)
- [生产部署](docs/operations/deploy.md)
- [员工额度与自助 Key](docs/employee-quota.md)
- [员工自助门户](docs/portal.md)
