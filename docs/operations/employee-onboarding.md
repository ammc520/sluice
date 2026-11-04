# 员工接入指南

公司所有大模型 API 调用统一经过内部网关。网关负责身份校验、额度与限流、敏感数据防泄漏（DLP）和审计。请不要再使用此前分发的上游厂商 Key，也不要直连厂商地址。

管理员会提供两样东西：

1. **网关地址**，例如 `https://api.example.com`
2. **公司 Token**，形如 `sk-company-xxxxxxxx`（只展示一次，请立即保存到密码管理器）

Token 与个人账号绑定，不要共享。丢失或泄露时联系管理员轮换。

Key 也可以通过员工门户自助创建：用管理员发放的工号与初始密码登录 `https://api.example.com/portal/login`，首次登录需修改密码；随后在「我的 Key」中创建具名 Key，明文只展示一次。同一员工的多个 Key 共享个人额度；额度不足时在「额度提额」中提交申请，管理员批准后立即生效，原 Key 可继续使用。详见 [员工自助门户](../portal.md)。

## 1. 接口地址

| 协议 | Base URL | 说明 |
|---|---|---|
| OpenAI 兼容 | `https://api.example.com/v1` | OpenAI SDK、DeepSeek SDK、兼容 OpenAI 的工具 |
| Anthropic | `https://api.example.com` | Anthropic SDK 会自动追加 `/v1/messages` |

网关不做 OpenAI 与 Anthropic 协议互转：使用 OpenAI SDK 调 OpenAI/DeepSeek 模型，使用 Anthropic SDK 调 Claude 模型。当前只支持纯文本请求，不支持图片、音频、文件等多模态输入。

模型名称必须使用管理员配置的路由名称（例如 `<OPENAI_MODEL>`、`<CLAUDE_MODEL>`、`<DEEPSEEK_MODEL>`）。不要自行拼接厂商模型名。

## 2. 认证方式

网关接受以下任一请求头：

```http
Authorization: Bearer sk-company-xxxxxxxx
```

或：

```http
x-api-key: sk-company-xxxxxxxx
```

SDK 会自动选择其中一种。不要同时发送两个不同的 Token，否则会返回 401。

## 3. OpenAI / DeepSeek SDK

### Python

```bash
pip install openai
```

```python
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["COMPANY_API_KEY"],
    base_url=os.environ["GATEWAY_BASE_URL"] + "/v1",
)

# 非流式
resp = client.chat.completions.create(
    model=os.environ["OPENAI_MODEL"],
    messages=[{"role": "user", "content": "用一句话解释什么是 API 网关"}],
)
print(resp.choices[0].message.content)

# 流式
stream = client.chat.completions.create(
    model=os.environ["OPENAI_MODEL"],
    messages=[{"role": "user", "content": "写一段 100 字的项目介绍"}],
    stream=True,
)
for chunk in stream:
    delta = chunk.choices[0].delta.content
    if delta:
        print(delta, end="", flush=True)
print()
```

DeepSeek 使用同一个 OpenAI SDK，只需把 `model` 换成管理员提供的 DeepSeek 路由名：

```python
client = OpenAI(
    api_key=os.environ["COMPANY_API_KEY"],
    base_url=os.environ["GATEWAY_BASE_URL"] + "/v1",
)
resp = client.chat.completions.create(
    model=os.environ["DEEPSEEK_MODEL"],
    messages=[{"role": "user", "content": "你好"}],
)
```

### Node.js / TypeScript

```bash
npm install openai
```

```ts
import OpenAI from 'openai'

const client = new OpenAI({
  apiKey: process.env.COMPANY_API_KEY,
  baseURL: `${process.env.GATEWAY_BASE_URL}/v1`,
})

const stream = await client.chat.completions.create({
  model: process.env.OPENAI_MODEL!,
  messages: [{ role: 'user', content: '你好，请自我介绍' }],
  stream: true,
})

for await (const chunk of stream) {
  process.stdout.write(chunk.choices[0]?.delta?.content ?? '')
}
```

## 4. Anthropic / Claude SDK

### Python

```bash
pip install anthropic
```

```python
import os
from anthropic import Anthropic

client = Anthropic(
    api_key=os.environ["COMPANY_API_KEY"],
    base_url=os.environ["GATEWAY_BASE_URL"],  # 不要加 /v1
)

# 非流式
message = client.messages.create(
    model=os.environ["CLAUDE_MODEL"],
    max_tokens=1024,
    messages=[{"role": "user", "content": "用三句话介绍 PostgreSQL"}],
)
print(message.content[0].text)

# 流式
with client.messages.stream(
    model=os.environ["CLAUDE_MODEL"],
    max_tokens=1024,
    messages=[{"role": "user", "content": "写一个简短的项目背景"}],
) as stream:
    for text in stream.text_stream:
        print(text, end="", flush=True)
print()
```

### Node.js / TypeScript

```bash
npm install @anthropic-ai/sdk
```

```ts
import Anthropic from '@anthropic-ai/sdk'

const client = new Anthropic({
  apiKey: process.env.COMPANY_API_KEY,
  baseURL: process.env.GATEWAY_BASE_URL, // 不要加 /v1
})

const stream = client.messages.stream({
  model: process.env.CLAUDE_MODEL!,
  max_tokens: 1024,
  messages: [{ role: 'user', content: '你好' }],
})

stream.on('text', (text) => process.stdout.write(text))
await stream.finalMessage()
```

## 5. curl 自检

OpenAI 兼容：

```bash
curl -sS "${GATEWAY_BASE_URL}/v1/chat/completions" \
  -H "Authorization: Bearer ${COMPANY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "'"${OPENAI_MODEL}"'",
    "messages": [{"role": "user", "content": "ping"}]
  }'
```

Anthropic：

```bash
curl -sS "${GATEWAY_BASE_URL}/v1/messages" \
  -H "x-api-key: ${COMPANY_API_KEY}" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "'"${CLAUDE_MODEL}"'",
    "max_tokens": 128,
    "messages": [{"role": "user", "content": "ping"}]
  }'
```

流式请求在 JSON 中加 `"stream": true`，客户端按 SSE 逐块读取。网关会原样透传上游分块，不额外缓冲。

## 6. 安全与合规

请求会经过 DLP 检测，可能发生：

| DLP 动作 | 员工看到的结果 |
|---|---|
| 脱敏（Mask） | 请求正常返回 200，但发送给模型的敏感片段已被替换（如手机号变成 `***`） |
| 阻断（Block） | 返回 403，`error.code` 为 `dlp_blocked`，并列出命中的规则；请求不会到达上游 |
| 仅审计（Audit） | 请求正常返回，命中记录进入审计并可能触发管理员邮件告警 |

- 不要在提示词、代码或日志里粘贴身份证号、银行卡号、手机号、云厂商 AK/SK、私钥、内部 IP 等敏感信息。即使被脱敏，调用仍会留下加密审计记录。
- 请求和响应原文会加密保存（默认最多保留 256 KiB），只有超级管理员在填写原因后才能解密查看，且该操作本身会写入管理员审计日志。
- 不要把 Token 写进代码仓库、前端代码、截图或聊天记录。使用环境变量或密钥管理工具。
- 不要把 `base_url` 临时改回厂商地址“做对比测试”；这属于绕过审计，旧 Key 会被撤销，且违反公司规定。
- 公司网络开启 IP 白名单时，需在公司网络或 VPN 内调用。

## 7. 常见状态码

| 状态码 | 含义 | 处理 |
|---|---|---|
| 401 | Token 无效、过期、被禁用，或员工账号被禁用 | 检查 Token 是否完整；联系管理员 |
| 402 | 今日/本月/总额度或请求数已用尽 | 联系管理员调整额度 |
| 403 | DLP 阻断，或来源 IP 不在白名单 | 按响应中的规则修改内容；确认网络/VPN |
| 413 | 请求体超过 2 MiB | 缩小输入 |
| 415 | 非文本内容或多模态请求 | 只发送文本 |
| 429 | 每分钟请求数超限 | 按 `Retry-After` 等待后重试 |
| 501 | 该端点未开放 | 使用 `/v1/chat/completions`、`/v1/messages`、`/v1/models` 等受支持端点 |
| 502/504 | 上游厂商错误或超时 | 稍后重试；持续出现请联系管理员 |
| 503 | 网关依赖不可用（数据库等） | 稍后重试；不要绕过网关直连 |

错误响应示例：

```json
{
  "error": {
    "code": "dlp_blocked",
    "message": "Request blocked by data loss prevention policy",
    "request_id": "…",
    "rules": ["云厂商 AK/SK"]
  }
}
```

联系管理员时请提供 `request_id`，不要粘贴完整敏感原文。
