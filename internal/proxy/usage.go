package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
)

// StreamParser consumes arbitrary SSE chunks and reports final usage.
type StreamParser interface {
	Feed(line []byte)
	Usage() Usage
	Done() bool
}

// NewStreamParser creates a parser for the given wire protocol.
func NewStreamParser(protocol string) StreamParser {
	return &streamUsageParser{protocol: protocol}
}

// maxSSELine bounds buffered partial SSE lines from a misbehaving upstream.
const maxSSELine = 1 << 20

type streamUsageParser struct {
	protocol string
	buf      []byte
	usage    Usage
	reported bool
	done     bool
}

func (p *streamUsageParser) Feed(chunk []byte) {
	p.buf = append(p.buf, chunk...)
	for {
		index := bytes.IndexByte(p.buf, '\n')
		if index < 0 {
			break
		}
		line := p.buf[:index]
		p.buf = p.buf[index+1:]
		p.handleLine(strings.TrimSuffix(string(line), "\r"))
	}
	if len(p.buf) > maxSSELine {
		p.buf = p.buf[:0]
	}
}

func (p *streamUsageParser) handleLine(line string) {
	if strings.HasPrefix(line, "event:") {
		if strings.TrimSpace(strings.TrimPrefix(line, "event:")) == "message_stop" {
			p.done = true
		}
		return
	}
	if !strings.HasPrefix(line, "data:") {
		return
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "" {
		return
	}
	if data == "[DONE]" {
		p.done = true
		return
	}
	if p.protocol == "anthropic" {
		p.parseAnthropic(data)
		return
	}
	p.parseOpenAI(data)
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (p *streamUsageParser) parseOpenAI(data string) {
	var event struct {
		Usage *openAIUsage `json:"usage"`
	}
	if json.Unmarshal([]byte(data), &event) != nil || event.Usage == nil {
		return
	}
	p.reported = true
	p.usage.PromptTokens = event.Usage.PromptTokens
	p.usage.CompletionTokens = event.Usage.CompletionTokens
	p.usage.TotalTokens = event.Usage.TotalTokens
}

type anthropicUsage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}

func (p *streamUsageParser) parseAnthropic(data string) {
	var event struct {
		Type    string `json:"type"`
		Message *struct {
			Usage *anthropicUsage `json:"usage"`
		} `json:"message"`
		Usage *anthropicUsage `json:"usage"`
	}
	if json.Unmarshal([]byte(data), &event) != nil {
		return
	}
	if event.Type == "message_stop" {
		p.done = true
	}
	if event.Message != nil {
		p.mergeAnthropicUsage(event.Message.Usage)
	}
	p.mergeAnthropicUsage(event.Usage)
}

func (p *streamUsageParser) mergeAnthropicUsage(usage *anthropicUsage) {
	if usage == nil {
		return
	}
	if usage.InputTokens != nil {
		p.reported = true
		p.usage.PromptTokens = *usage.InputTokens
	}
	if usage.OutputTokens != nil {
		p.reported = true
		p.usage.CompletionTokens = *usage.OutputTokens
	}
}

func (p *streamUsageParser) Usage() Usage {
	usage := p.usage
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if !p.reported || usage.TotalTokens == 0 {
		usage.Estimated = true
	}
	return usage
}

func (p *streamUsageParser) Done() bool { return p.done }

// parseUsage extracts token counts from an OpenAI or Anthropic JSON response.
// Estimated is set when the upstream did not report usage.
func parseUsage(body []byte, protocol string) Usage {
	var payload struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Usage{Estimated: true}
	}
	u := payload.Usage
	usage := Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
	if usage.PromptTokens == 0 && u.InputTokens > 0 {
		usage.PromptTokens = u.InputTokens
	}
	if usage.CompletionTokens == 0 && u.OutputTokens > 0 {
		usage.CompletionTokens = u.OutputTokens
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if usage.TotalTokens == 0 {
		usage.Estimated = true
	}
	return usage
}
