package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// openaiConv speaks the OpenAI chat-completions API (streaming, function tools). It covers OpenAI
// itself and every compatible server: Azure OpenAI, OpenRouter, Groq, LM Studio, vLLM, Ollama.
type openaiConv struct {
	cfg      Config
	messages []map[string]any
	tools    []map[string]any
	http     *http.Client
}

func newOpenAI(cfg Config, system string, history []Turn, user string, tools []ToolDef) Conversation {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	c := &openaiConv{cfg: cfg, http: &http.Client{Timeout: 10 * time.Minute}}
	c.messages = append(c.messages, map[string]any{"role": "system", "content": system})
	for _, h := range history {
		c.messages = append(c.messages, map[string]any{"role": h.Role, "content": h.Text})
	}
	c.messages = append(c.messages, map[string]any{"role": "user", "content": user})
	for _, t := range tools {
		c.tools = append(c.tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
	}
	return c
}

type oaChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *openaiConv) Step(ctx context.Context, onText func(string)) (*Response, error) {
	body := map[string]any{
		"model": c.cfg.Model, "messages": c.messages, "stream": true,
		"max_tokens": c.cfg.MaxTokens, "stream_options": map[string]any{"include_usage": true},
	}
	if len(c.tools) > 0 {
		body["tools"] = c.tools
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("%s: %s", res.Status, apiError(msg))
	}
	r := &Response{Stop: StopEnd}
	type acc struct{ id, name, args string }
	calls := map[int]*acc{}
	var text strings.Builder
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ch oaChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			continue
		}
		if ch.Error != nil {
			return nil, fmt.Errorf("provider error: %s", ch.Error.Message)
		}
		if ch.Usage != nil {
			r.InputTokens, r.OutputTokens = ch.Usage.PromptTokens, ch.Usage.CompletionTokens
		}
		for _, choice := range ch.Choices {
			if choice.Delta.Content != "" {
				text.WriteString(choice.Delta.Content)
				onText(choice.Delta.Content)
			}
			for _, tc := range choice.Delta.ToolCalls {
				a := calls[tc.Index]
				if a == nil {
					a = &acc{}
					calls[tc.Index] = a
				}
				if tc.ID != "" {
					a.id = tc.ID
				}
				if tc.Function.Name != "" {
					a.name = tc.Function.Name
				}
				a.args += tc.Function.Arguments
			}
			switch choice.FinishReason {
			case "length":
				r.Stop = StopMaxTokens
			case "content_filter":
				r.Stop = StopRefusal
			case "tool_calls":
				r.Stop = StopToolUse
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	r.Text = text.String()
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	var toolCalls []map[string]any
	for _, i := range idx {
		a := calls[i]
		if a.id == "" {
			a.id = fmt.Sprintf("call_%d", i)
		}
		if a.args == "" {
			a.args = "{}"
		}
		r.ToolCalls = append(r.ToolCalls, ToolCall{ID: a.id, Name: a.name, Input: json.RawMessage(a.args)})
		toolCalls = append(toolCalls, map[string]any{"id": a.id, "type": "function", "function": map[string]any{"name": a.name, "arguments": a.args}})
	}
	if r.Stop == StopRefusal {
		return r, nil
	}
	if len(r.ToolCalls) > 0 && r.Stop == StopEnd {
		r.Stop = StopToolUse // some servers report "stop" even when they called tools
	}
	am := map[string]any{"role": "assistant", "content": r.Text}
	if len(toolCalls) > 0 {
		am["tool_calls"] = toolCalls
	}
	c.messages = append(c.messages, am)
	return r, nil
}

func (c *openaiConv) AddToolResults(results []ToolResult) {
	for _, r := range results {
		content := r.Content
		if r.IsError {
			content = "ERROR: " + content
		}
		c.messages = append(c.messages, map[string]any{"role": "tool", "tool_call_id": r.ID, "content": content})
	}
}

func apiError(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return strings.TrimSpace(string(body))
}
