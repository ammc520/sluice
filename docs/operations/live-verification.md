# 真实上游验收清单

本文用于在生产部署完成后，用真实 OpenAI、Anthropic（Claude）、DeepSeek 账号验证网关的转发、流式、审计、额度与 DLP 行为。所有步骤只使用测试用最小请求，不要粘贴真实业务数据。

> 验收前确认：`.env.production` 已按 [deploy.md](deploy.md) 配置；网关 `/readyz` 返回 `{"status":"ready"}`；管理员已完成首次登录并删除 `BOOTSTRAP_ADMIN_PASSWORD`。

## 1. 准备三家上游

登录管理后台，在「上游供应商」中各创建一个供应商（API Key 只在创建/更新时提交，页面之后只显示末四位）：

| 供应商 | 协议 | Base URL（按实际区域调整） | 模型示例 |
|---|---|---|---|
| OpenAI | `openai_compatible` | `https://api.openai.com/v1` | `gpt-4o-mini` |
| Anthropic | `anthropic` | `https://api.anthropic.com` | `claude-3-5-haiku-latest` |
| DeepSeek | `openai_compatible` | `https://api.deepseek.com/v1` | `deepseek-chat` |

在「模型路由」中为每个模型创建 `exact` 路由，并确认供应商为启用状态。第一版每个供应商只维护一个聊天模型；`/v1/models`、`/v1/embeddings` 等端点按需另行验证。

创建一名测试员工和公司 Token，配额建议设为 `tokens / total / 100000`，速率限制 `60 RPM`。Token 明文只在创建时显示一次，立即保存到测试密码库。

## 2. 一键状态检查

仓库根目录执行：

```bash
export GATEWAY_URL='https://api.example.com'
export COMPANY_TOKEN='sk-company-...'    # 仅测试 Token
export OPENAI_MODEL='gpt-4o-mini'
export ANTHROPIC_MODEL='claude-3-5-haiku-latest'
export DEEPSEEK_MODEL='deepseek-chat'
./scripts/live_check.sh
```

脚本分别向 `/v1/chat/completions`（OpenAI、DeepSeek）和 `/v1/messages`（Anthropic）发送一次非流式最小请求，只输出 HTTP 状态，不打印 Token 或响应正文。预期：

```
openai (gpt-4o-mini)         HTTP 200 PASS
deepseek (deepseek-chat)     HTTP 200 PASS
anthropic (claude-3-5-haiku-latest) HTTP 200 PASS
```

任意一行非 200 时，先在管理后台「审计日志」用同一时间范围查看 `status_code`、`error_code`，再检查供应商 Base URL、Key 末四位和模型路由。

## 3. 流式与非流式（curl）

OpenAI 兼容（OpenAI / DeepSeek）：

```bash
# 非流式
curl -sS https://api.example.com/v1/chat/completions \
  -H "Authorization: Bearer $COMPANY_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Reply with OK"}],"max_tokens":8}'

# 流式：应逐块出现 data:，最后出现 data: [DONE]
curl -N -sS https://api.example.com/v1/chat/completions \
  -H "Authorization: Bearer $COMPANY_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Count 1 to 5"}],"max_tokens":32,"stream":true}'
```

Anthropic：

```bash
curl -sS https://api.example.com/v1/messages \
  -H "x-api-key: $COMPANY_TOKEN" \
  -H 'anthropic-version: 2023-06-01' \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-3-5-haiku-latest","max_tokens":16,"messages":[{"role":"user","content":"Reply with OK"}]}'

curl -N -sS https://api.example.com/v1/messages \
  -H "x-api-key: $COMPANY_TOKEN" \
  -H 'anthropic-version: 2023-06-01' \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-3-5-haiku-latest","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"Count 1 to 5"}]}'
```

对每种协议各做一次流式和非流式。流式响应必须保持 `Content-Type: text/event-stream`，不能缓冲到结束才一次返回；可通过 `curl -N` 观察分块到达。

## 4. SDK 指向网关

| SDK | 配置 |
|---|---|
| OpenAI（Python/Node） | `base_url="https://api.example.com/v1"`，`api_key=COMPANY_TOKEN` |
| DeepSeek（OpenAI 兼容 SDK） | `base_url="https://api.example.com/v1"`，`api_key=COMPANY_TOKEN` |
| Anthropic（Python/Node） | `base_url="https://api.example.com"`，`api_key=COMPANY_TOKEN` |

Anthropic SDK 会在 base URL 后追加 `/v1/messages`，因此 base URL 不要带 `/v1`。OpenAI 兼容 SDK 需要带 `/v1`。不要配置任何真实上游 Key 到员工环境变量。

## 5. 后台与审计检查

每次请求后等待 1–2 秒（审计异步写入），在管理后台逐项核对：

1. **审计日志**：能看到请求 ID、员工、Token 末四位（不显示明文）、模型、状态码、延迟、Prompt/Response Token。
2. **用量与额度**：Token 的已用额度增加，增量等于上游返回的 `usage.total_tokens`；流式请求同样应写入最终 usage，而不是预留值。
3. **流式审计**：`stream: true` 的记录 `total_tokens` 与上游一致，响应内容截断标记仅在超过 256 KiB 时出现。
4. **超级管理员查看原文**：填写不少于 5 个字符的理由后可见 Prompt/Response；「管理员操作日志」出现对应 `reveal` 记录。普通 `operator` / `auditor` 调用 reveal 必须返回 403。
5. **审计保留**：`AUDIT_MAINTENANCE_DATABASE_URL` 已配置时，保留任务使用独立连接；应用角色不应拥有 `audit_logs` 的 `DELETE` 权限。

## 6. DLP 三动作

使用一次性测试内容（不要使用真实个人信息）：

| 动作 | 测试输入 | 预期 |
|---|---|---|
| Mask | 含 `13800138000` 的 Prompt | 客户端收到 200；审计 `dlp_action=mask`；上游实际收到 `***`；审计原文（reveal）仍保留原号码 |
| Block | 含 `AKIAIOSFODNN7EXAMPLE` 的 Prompt | 客户端收到 403 `dlp_blocked`；上游无调用；审计 `dlp_action=block` |
| Audit | 含 `alice@company.example` 的 Prompt | 客户端收到 200；上游收到原文；审计 `dlp_action=audit` |

为验证邮件告警：在「DLP 规则」中启用一条 `audit`/`block` 规则并勾选告警，配置 SMTP 后触发两次相同命中。预期只收到一封邮件，邮件正文只含时间、员工、规则、动作、请求 ID，不含 Prompt 原文。随后在「设置 → SMTP」使用测试发送确认链路。

## 7. 认证、额度与限流

| 场景 | 预期 |
|---|---|
| 不带 Token | 401 `invalid_token` |
| 篡改 Token 或停用员工 | 401 |
| Token 不在 IP 白名单 | 401 |
| 额度耗尽（将测试 Token 的 `quota_total` 调到已用量） | 402 `quota_exceeded` |
| 连续超过 RPM | 429，带 `Retry-After` |
| 请求体超过 2 MiB | 413 |
| 图片/文件/音频 content block | 415 |
| 未支持端点（如 `/v1/audio/speech`） | 501 |
| 上游断开连接 | 502 |
| 上游超过供应商超时 | 504 |

## 8. 旧 Key 撤销

切换完成后必须撤销此前自由分发的上游 Key，避免员工绕过网关：

1. 在 OpenAI / Anthropic / DeepSeek 控制台为旧 Key 设置过期或直接删除，只保留写入网关供应商配置的新 Key。
2. 用旧 Key 直接请求上游（不经过网关），预期返回 401/403。
3. 用公司 Token 经网关请求同一模型，预期 200。
4. 在仓库、服务器和员工文档中搜索旧 Key 片段，确认没有残留：

   ```bash
   grep -R --exclude-dir=.git --exclude-dir=node_modules -n 'OLD_KEY_PREFIX' .
   ```

   不要执行会打印完整密钥的命令；只检查前缀/末四位。
5. 确认网关日志与审计表中没有上游明文 Key：供应商 Key 只应以 AES-256-GCM 密文存在于 `upstream_providers.api_key_enc`。

## 9. 本地自动化基线

`internal/e2e` 使用真实 HTTP 栈、临时 PostgreSQL、mock 上游完成以下验收，可在发布前重复执行：

```bash
export TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55432/gateway_test?sslmode=disable'
go test ./internal/e2e -count=1 -v
```

当前基线（2026-11-03，Windows 11 开发机，mock 上游，单次运行）：

| 项目 | 结果 |
|---|---|
| 100 并发流式请求 × 20 chunk | 全部 200，p95 约 242 ms，审计记录完整 |
| 网关附加延迟（10 KiB 请求，200 样本） | p95 约 3.3 ms（预算 10 ms） |
| DLP 检查（10 KiB JSON，200 样本） | p95 约 2.5 ms（预算 5 ms） |
| DLP Mask / Block / Audit | 通过 |
| 401 / 402 / 403 / 413 / 415 / 429 / 501 / 502 / 504 | 通过 |
| 告警去重（两次相同命中） | 只发送一封邮件 |
| 客户端断开 | 上游请求上下文被取消，审计标记断连 |

生产环境的绝对值会随上游网络和主机负载变化；上表只用于确认网关自身没有明显性能回归。

## 10. 验收记录

| 日期 | 环境 | 执行人 | OpenAI | Anthropic | DeepSeek | 旧 Key 已撤销 | 备注 |
|---|---|---|---|---|---|---|---|
| | | | | | | | |
