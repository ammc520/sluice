/**
 * Presentation helpers for audit plaintext. These only reshape content that
 * the API has already decrypted for a super admin; callers must still clear
 * every result when the dialog closes.
 */

type JsonObject = Record<string, unknown>

export interface PromptMessage {
  role: string
  text: string
}

export interface PromptView {
  /** Anthropic-style top-level system prompt. */
  system: string
  messages: PromptMessage[]
  parsed: boolean
}

export interface ResponseView {
  text: string
  thinking: string
  model: string
  stopReason: string
  inputTokens: number | null
  outputTokens: number | null
  cacheReadTokens: number | null
  cacheCreationTokens: number | null
  /** True when the payload was a stream captured as SSE. */
  streamed: boolean
  parsed: boolean
}

interface TokenUsage {
  inputTokens: number | null
  outputTokens: number | null
  cacheReadTokens: number | null
  cacheCreationTokens: number | null
}

function isObject(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function asNumber(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function contentToText(content: unknown, depth = 0): string {
  if (typeof content === 'string') return content
  if (Array.isArray(content)) {
    return content
      .map((item) => contentToText(item, depth + 1))
      .filter((item) => item.trim().length > 0)
      .join('\n\n')
  }
  if (!isObject(content)) return ''

  const type = asString(content.type)
  if (type === 'text') return asString(content.text)
  if (type === 'thinking' || type === 'redacted_thinking') return ''
  if (type === 'image') return '[图片]'
  if (type === 'document') return '[文档]'
  if (type === 'tool_use') {
    return `[工具调用：${asString(content.name) || '未命名'}]`
  }
  if (type === 'tool_result') {
    const inner = contentToText(content.content, depth + 1)
    return inner ? `[工具结果]\n${inner}` : '[工具结果]'
  }
  if (typeof content.text === 'string') return content.text
  return ''
}

function normalizeRole(value: unknown): string {
  const role = asString(value).trim().toLowerCase()
  if (role === 'human') return 'user'
  if (role === 'ai' || role === 'model') return 'assistant'
  if (role === 'developer') return 'system'
  return role || 'unknown'
}

/** Parses a stored prompt into role-labelled messages when possible. */
export function formatPrompt(raw: string): PromptView {
  const empty: PromptView = { system: '', messages: [], parsed: false }
  const trimmed = raw.trim()
  if (!trimmed) return empty

  let data: unknown
  try {
    data = JSON.parse(trimmed)
  } catch {
    return empty
  }
  if (!isObject(data)) return empty

  const system = contentToText(data.system).trim()
  let source: unknown[] = []
  if (Array.isArray(data.messages)) source = data.messages
  else if (Array.isArray(data.input)) source = data.input

  const messages: PromptMessage[] = []
  for (const item of source) {
    if (!isObject(item)) continue
    const text = contentToText(item.content).trim()
    if (!text) continue
    messages.push({ role: normalizeRole(item.role), text })
  }

  if (messages.length === 0 && typeof data.prompt === 'string' && data.prompt.trim()) {
    messages.push({ role: 'user', text: data.prompt })
  }
  if (messages.length === 0 && typeof data.input === 'string' && data.input.trim()) {
    messages.push({ role: 'user', text: data.input })
  }

  if (!system && messages.length === 0) return empty
  return { system, messages, parsed: true }
}

function readUsage(data: JsonObject): TokenUsage {
  const usage = isObject(data.usage) ? data.usage : {}
  const details = isObject(usage.prompt_tokens_details)
    ? usage.prompt_tokens_details
    : null
  return {
    inputTokens:
      asNumber(usage.input_tokens) ?? asNumber(usage.prompt_tokens),
    outputTokens:
      asNumber(usage.output_tokens) ?? asNumber(usage.completion_tokens),
    cacheReadTokens:
      asNumber(usage.cache_read_input_tokens) ??
      asNumber(details?.cached_tokens),
    cacheCreationTokens: asNumber(usage.cache_creation_input_tokens),
  }
}

function mergeUsage(target: TokenUsage, usage: unknown): void {
  if (!isObject(usage)) return
  const details = isObject(usage.prompt_tokens_details)
    ? usage.prompt_tokens_details
    : null
  const input =
    asNumber(usage.input_tokens) ?? asNumber(usage.prompt_tokens)
  const output =
    asNumber(usage.output_tokens) ?? asNumber(usage.completion_tokens)
  const cacheRead =
    asNumber(usage.cache_read_input_tokens) ??
    asNumber(details?.cached_tokens)
  const cacheCreation = asNumber(usage.cache_creation_input_tokens)
  if (input !== null) target.inputTokens = input
  if (output !== null) target.outputTokens = output
  if (cacheRead !== null) target.cacheReadTokens = cacheRead
  if (cacheCreation !== null) target.cacheCreationTokens = cacheCreation
}

function emptyUsage(): TokenUsage {
  return {
    inputTokens: null,
    outputTokens: null,
    cacheReadTokens: null,
    cacheCreationTokens: null,
  }
}

function emptyResponse(): ResponseView {
  return {
    ...emptyUsage(),
    text: '',
    thinking: '',
    model: '',
    stopReason: '',
    streamed: false,
    parsed: false,
  }
}

/** Extracts the final assistant text from a non-stream JSON response. */
function extractMessage(data: unknown): ResponseView | null {
  if (!isObject(data)) return null

  const usage = readUsage(data)
  const textParts: string[] = []
  const thinkingParts: string[] = []
  let model = asString(data.model)
  let stopReason = asString(data.stop_reason)

  if (Array.isArray(data.content)) {
    for (const block of data.content) {
      if (!isObject(block)) continue
      if (block.type === 'text') textParts.push(asString(block.text))
      else if (block.type === 'thinking') {
        thinkingParts.push(asString(block.thinking))
      }
    }
  }

  if (Array.isArray(data.choices)) {
    const choice = data.choices[0]
    if (isObject(choice)) {
      const message = isObject(choice.message) ? choice.message : null
      if (message) {
        textParts.push(contentToText(message.content))
        thinkingParts.push(
          asString(message.reasoning_content),
          asString(message.reasoning),
        )
      }
      if (typeof choice.finish_reason === 'string') {
        stopReason = choice.finish_reason
      }
    }
  }

  let text = textParts.filter(Boolean).join('\n\n').trim()
  const thinking = thinkingParts.filter(Boolean).join('\n\n').trim()
  if (!text && isObject(data.error)) {
    text = `[上游错误] ${asString(data.error.message) || '未知错误'}`
  }
  if (!text && !thinking && !isObject(data.error)) return null

  return {
    ...usage,
    text,
    thinking,
    model,
    stopReason,
    streamed: false,
    parsed: true,
  }
}

/** Extracts text deltas from a captured SSE stream. */
function extractStream(raw: string): ResponseView | null {
  const usage = emptyUsage()
  const textParts: string[] = []
  const thinkingParts: string[] = []
  let model = ''
  let stopReason = ''
  let seen = false

  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim()
    if (!trimmed.startsWith('data:')) continue
    const payload = trimmed.slice(5).trim()
    if (!payload || payload === '[DONE]') continue

    let event: unknown
    try {
      event = JSON.parse(payload)
    } catch {
      continue
    }
    if (!isObject(event)) continue
    seen = true

    const type = asString(event.type)
    if (type === 'message_start') {
      const message = isObject(event.message) ? event.message : null
      if (message) {
        model ||= asString(message.model)
        mergeUsage(usage, message.usage)
      }
    } else if (type === 'content_block_start') {
      const block = isObject(event.content_block) ? event.content_block : null
      if (block) {
        if (block.type === 'text') textParts.push(asString(block.text))
        else if (block.type === 'thinking') {
          thinkingParts.push(asString(block.thinking))
        }
      }
    } else if (type === 'content_block_delta') {
      const delta = isObject(event.delta) ? event.delta : null
      if (delta) {
        if (delta.type === 'thinking_delta') {
          thinkingParts.push(asString(delta.thinking))
        } else if (typeof delta.text === 'string') {
          textParts.push(delta.text)
        }
      }
    } else if (type === 'message_delta') {
      const delta = isObject(event.delta) ? event.delta : null
      if (delta && typeof delta.stop_reason === 'string') {
        stopReason = delta.stop_reason
      }
      mergeUsage(usage, event.usage)
    } else if (Array.isArray(event.choices)) {
      model ||= asString(event.model)
      mergeUsage(usage, event.usage)
      const choice = event.choices[0]
      if (isObject(choice)) {
        const delta = isObject(choice.delta) ? choice.delta : null
        if (delta) {
          if (typeof delta.content === 'string') textParts.push(delta.content)
          if (typeof delta.reasoning_content === 'string') {
            thinkingParts.push(delta.reasoning_content)
          }
          if (typeof delta.reasoning === 'string') {
            thinkingParts.push(delta.reasoning)
          }
        }
        if (typeof choice.finish_reason === 'string') {
          stopReason = choice.finish_reason
        }
      }
    }
  }

  if (!seen) return null
  return {
    ...usage,
    text: textParts.filter(Boolean).join('').trim(),
    thinking: thinkingParts.filter(Boolean).join('').trim(),
    model,
    stopReason,
    streamed: true,
    parsed: true,
  }
}

/** Parses a stored response from either JSON or captured SSE frames. */
export function formatResponse(raw: string): ResponseView {
  const trimmed = raw.trim()
  if (!trimmed) return emptyResponse()

  const streamed = extractStream(trimmed)
  if (streamed) return streamed

  try {
    const parsed = extractMessage(JSON.parse(trimmed))
    if (parsed) return parsed
  } catch {
    // Not JSON; fall through to raw rendering.
  }
  return emptyResponse()
}

/** Pretty-prints JSON without throwing on captured streams or plain text. */
export function prettyJSON(raw: string): string {
  const trimmed = raw.trim()
  if (!trimmed) return ''
  try {
    const parsed: unknown = JSON.parse(trimmed)
    if (parsed !== null && typeof parsed === 'object') {
      return JSON.stringify(parsed, null, 2)
    }
  } catch {
    // Keep the original payload.
  }
  return raw
}
