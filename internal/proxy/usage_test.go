package proxy

import "testing"

func TestOpenAIStreamParserUsageAcrossChunks(t *testing.T) {
	p := NewStreamParser("openai_compatible")
	chunks := []string{
		`data: {"choices":[{"delta":{"content":"he"}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"content":"llo"}}]}` + "\n\n",
		`data: {"usage":{"prompt_tokens":5,"completion_tokens":6,"total_tokens":11}}` + "\n\n",
		"data: [DONE]\n\n",
	}
	for _, chunk := range chunks {
		p.Feed([]byte(chunk))
	}
	usage := p.Usage()
	if usage.TotalTokens != 11 || usage.PromptTokens != 5 || usage.CompletionTokens != 6 || usage.Estimated {
		t.Fatalf("%+v", usage)
	}
	if !p.Done() {
		t.Fatal("[DONE] not recognized")
	}
}

func TestAnthropicStreamParserUsage(t *testing.T) {
	p := NewStreamParser("anthropic")
	p.Feed([]byte("event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":9}}}\n\n"))
	p.Feed([]byte("event: message_delta\ndata: {\"usage\":{\"output_tokens\":4}}\n\n"))
	usage := p.Usage()
	if usage.PromptTokens != 9 || usage.CompletionTokens != 4 || usage.TotalTokens != 13 || usage.Estimated {
		t.Fatalf("%+v", usage)
	}
	p.Feed([]byte("event: message_stop\ndata: {}\n\n"))
	if !p.Done() {
		t.Fatal("message_stop not recognized")
	}
}

func TestStreamParserHandlesArbitraryChunkBoundaries(t *testing.T) {
	p := NewStreamParser("openai_compatible")
	payload := "data: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n"
	for i := range payload {
		p.Feed([]byte{payload[i]})
	}
	if got := p.Usage(); got.TotalTokens != 5 || got.Estimated {
		t.Fatalf("%+v", got)
	}
}

func TestStreamParserWithoutUsageIsEstimated(t *testing.T) {
	p := NewStreamParser("openai_compatible")
	p.Feed([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"))
	p.Feed([]byte("data: [DONE]\n\n"))
	if got := p.Usage(); !got.Estimated || got.TotalTokens != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestParseUsage(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		protocol string
		want     Usage
	}{
		{"openai", `{"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`, "openai_compatible", Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}},
		{"anthropic", `{"usage":{"input_tokens":3,"output_tokens":4}}`, "anthropic", Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}},
		{"missing", `{"id":"x"}`, "openai_compatible", Usage{Estimated: true}},
		{"invalid", `not json`, "openai_compatible", Usage{Estimated: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseUsage([]byte(tc.body), tc.protocol); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}
