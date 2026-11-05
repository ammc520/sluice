import { describe, expect, it } from 'vitest'

import { formatPrompt, formatResponse, prettyJSON } from './promptFormat'

describe('formatPrompt', () => {
  it('extracts Anthropic system, messages and non-text blocks', () => {
    const raw = JSON.stringify({
      model: 'deepseek-flash',
      system: [{ type: 'text', text: '你是助手' }],
      messages: [
        {
          role: 'user',
          content: [
            { type: 'text', text: '看图' },
            { type: 'image', source: { type: 'base64', data: 'xxx' } },
          ],
        },
        {
          role: 'assistant',
          content: [{ type: 'tool_use', name: 'search' }],
        },
        {
          role: 'user',
          content: [{ type: 'tool_result', content: '结果摘要' }],
        },
      ],
    })

    const view = formatPrompt(raw)
    expect(view.parsed).toBe(true)
    expect(view.system).toBe('你是助手')
    expect(view.messages).toEqual([
      { role: 'user', text: '看图\n\n[图片]' },
      { role: 'assistant', text: '[工具调用：search]' },
      { role: 'user', text: '[工具结果]\n结果摘要' },
    ])
  })

  it('supports OpenAI string content and plain prompt fields', () => {
    expect(
      formatPrompt(
        JSON.stringify({
          messages: [
            { role: 'system', content: 'system rules' },
            { role: 'user', content: 'hello' },
          ],
        }),
      ).messages,
    ).toEqual([
      { role: 'system', text: 'system rules' },
      { role: 'user', text: 'hello' },
    ])

    expect(formatPrompt(JSON.stringify({ prompt: 'single prompt' })).messages).toEqual([
      { role: 'user', text: 'single prompt' },
    ])
  })

  it('rejects plain text and empty payloads', () => {
    expect(formatPrompt('just text')).toEqual({
      system: '',
      messages: [],
      parsed: false,
    })
    expect(formatPrompt('')).toEqual({ system: '', messages: [], parsed: false })
  })
})

describe('formatResponse', () => {
  it('extracts an Anthropic JSON message with thinking and usage', () => {
    const view = formatResponse(
      JSON.stringify({
        type: 'message',
        model: 'deepseek-v4-pro',
        stop_reason: 'end_turn',
        content: [
          { type: 'thinking', thinking: '先分析' },
          { type: 'text', text: '答案是 42' },
        ],
        usage: {
          input_tokens: 120,
          output_tokens: 30,
          cache_read_input_tokens: 100,
          cache_creation_input_tokens: 20,
        },
      }),
    )

    expect(view.parsed).toBe(true)
    expect(view.streamed).toBe(false)
    expect(view.text).toBe('答案是 42')
    expect(view.thinking).toBe('先分析')
    expect(view.model).toBe('deepseek-v4-pro')
    expect(view.stopReason).toBe('end_turn')
    expect(view.inputTokens).toBe(120)
    expect(view.outputTokens).toBe(30)
    expect(view.cacheReadTokens).toBe(100)
    expect(view.cacheCreationTokens).toBe(20)
  })

  it('extracts an OpenAI-compatible JSON completion', () => {
    const view = formatResponse(
      JSON.stringify({
        model: 'gpt-x',
        choices: [
          {
            message: { content: 'hi there', reasoning_content: 'pondered' },
            finish_reason: 'stop',
          },
        ],
        usage: {
          prompt_tokens: 10,
          completion_tokens: 5,
          prompt_tokens_details: { cached_tokens: 4 },
        },
      }),
    )

    expect(view.text).toBe('hi there')
    expect(view.thinking).toBe('pondered')
    expect(view.stopReason).toBe('stop')
    expect(view.inputTokens).toBe(10)
    expect(view.outputTokens).toBe(5)
    expect(view.cacheReadTokens).toBe(4)
  })

  it('reassembles an Anthropic SSE stream', () => {
    const raw = [
      'event: message_start',
      'data: {"type":"message_start","message":{"model":"deepseek-flash","usage":{"input_tokens":42,"cache_read_input_tokens":8}}}',
      '',
      'event: content_block_start',
      'data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}',
      '',
      'event: content_block_delta',
      'data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"想一想"}}',
      '',
      'event: content_block_start',
      'data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}',
      '',
      'event: content_block_delta',
      'data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"你"}}',
      '',
      'data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"好"}}',
      '',
      'data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}',
      '',
      'data: [DONE]',
      '',
    ].join('\n')

    const view = formatResponse(raw)
    expect(view.streamed).toBe(true)
    expect(view.text).toBe('你好')
    expect(view.thinking).toBe('想一想')
    expect(view.model).toBe('deepseek-flash')
    expect(view.stopReason).toBe('end_turn')
    expect(view.inputTokens).toBe(42)
    expect(view.outputTokens).toBe(9)
    expect(view.cacheReadTokens).toBe(8)
  })

  it('reassembles an OpenAI-compatible SSE stream', () => {
    const raw = [
      'data: {"model":"gpt-x","choices":[{"delta":{"reasoning_content":"嗯"}}]}',
      'data: {"model":"gpt-x","choices":[{"delta":{"content":"he"}}]}',
      'data: {"model":"gpt-x","choices":[{"delta":{"content":"llo"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}',
      'data: [DONE]',
    ].join('\n')

    const view = formatResponse(raw)
    expect(view.streamed).toBe(true)
    expect(view.text).toBe('hello')
    expect(view.thinking).toBe('嗯')
    expect(view.stopReason).toBe('stop')
    expect(view.inputTokens).toBe(3)
    expect(view.outputTokens).toBe(2)
  })

  it('falls back for plain text and surfaces upstream errors', () => {
    expect(formatResponse('not json').parsed).toBe(false)
    expect(
      formatResponse(JSON.stringify({ error: { message: 'rate limited' } })).text,
    ).toBe('[上游错误] rate limited')
  })
})

describe('prettyJSON', () => {
  it('indents objects while preserving streams and text', () => {
    expect(prettyJSON('{"a":1}')).toBe('{\n  "a": 1\n}')
    expect(prettyJSON('data: {}\n')).toBe('data: {}\n')
    expect(prettyJSON('plain')).toBe('plain')
  })
})
