// Package ai runs the Copilot: a provider-neutral tool loop over Claude (official SDK) or any
// OpenAI-compatible chat API (OpenAI, Azure OpenAI, OpenRouter, Groq, LM Studio, vLLM, Ollama).
// Tools are supplied by the caller; this package never touches the cluster itself.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Provider names.
const (
	Anthropic = "anthropic"
	OpenAI    = "openai"
	Ollama    = "ollama"
)

// DefaultModel per provider (the user can change it).
var DefaultModel = map[string]string{Anthropic: "claude-opus-5-5", OpenAI: "gpt-5", Ollama: "llama3.1"}

// Turn is a previous exchange of the conversation, as plain text.
type Turn struct {
	Role string `json:"role"` // user | assistant
	Text string `json:"text"`
}

// ToolDef describes a tool to the model. Schema is a JSON Schema object.
type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any
}

// ToolCall is a tool invocation requested by the model.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult answers a ToolCall.
type ToolResult struct {
	ID      string
	Content string
	IsError bool
}

// Stop reasons, normalized across providers.
const (
	StopEnd       = "end"
	StopToolUse   = "tool_use"
	StopMaxTokens = "max_tokens"
	StopRefusal   = "refusal"
)

// Response is one model step.
type Response struct {
	Text            string
	ToolCalls       []ToolCall
	Stop            string
	RefusalCategory string
	InputTokens     int64
	OutputTokens    int64
}

// Conversation is one provider-native transcript: it keeps exactly what the model produced
// (e.g. Claude's thinking blocks) between tool steps.
type Conversation interface {
	Step(ctx context.Context, onText func(string)) (*Response, error)
	AddToolResults(results []ToolResult)
}

// Config selects and parameterizes a provider.
type Config struct {
	Provider  string
	Model     string
	BaseURL   string // empty = official endpoint (anthropic/openai) or http://localhost:11434/v1 (ollama)
	APIKey    string
	Effort    string // anthropic only
	MaxTokens int64
}

// Start opens a conversation with a system prompt, previous turns, the new user message and tools.
func Start(cfg Config, system string, history []Turn, user string, tools []ToolDef) (Conversation, error) {
	if cfg.Model == "" {
		cfg.Model = DefaultModel[cfg.Provider]
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 64000
	}
	switch cfg.Provider {
	case Anthropic:
		if cfg.APIKey == "" {
			return nil, errors.New("no API key configured for Claude")
		}
		return newClaude(cfg, system, history, user, tools), nil
	case OpenAI, Ollama:
		if cfg.Provider == OpenAI && cfg.APIKey == "" && cfg.BaseURL == "" {
			return nil, errors.New("no API key configured for the OpenAI-compatible provider")
		}
		if cfg.Provider == Ollama && cfg.BaseURL == "" {
			cfg.BaseURL = "http://localhost:11434/v1"
		}
		if cfg.MaxTokens > 16000 {
			cfg.MaxTokens = 16000 // many compatible servers reject larger completion limits
		}
		return newOpenAI(cfg, system, history, user, tools), nil
	}
	return nil, fmt.Errorf("unknown AI provider %q", cfg.Provider)
}

// Tool is a tool the model may call.
type Tool struct {
	Def ToolDef
	Run func(ctx context.Context, input json.RawMessage) (string, error)
}

// Hooks observe the loop.
type Hooks struct {
	OnText func(delta string)
	OnTool func(call ToolCall)
}

// Result of a Run.
type Result struct {
	Steps           int
	Refused         bool
	RefusalCategory string
	InputTokens     int64
	OutputTokens    int64
}

// ErrTruncated means the model hit max_tokens in the middle of a tool call: the call is not run.
var ErrTruncated = errors.New("the model's answer was cut off (max_tokens) in the middle of a tool call")

// Run drives the tool loop until the model answers without tools, refuses, or maxSteps is reached.
// Tool errors go back to the model as error results; they never abort the loop.
func Run(ctx context.Context, conv Conversation, tools []Tool, maxSteps int, h Hooks) (*Result, error) {
	byName := map[string]Tool{}
	for _, t := range tools {
		byName[t.Def.Name] = t
	}
	res := &Result{}
	onText := h.OnText
	if onText == nil {
		onText = func(string) {}
	}
	for res.Steps < maxSteps {
		resp, err := conv.Step(ctx, onText)
		if err != nil {
			return res, err
		}
		res.Steps++
		res.InputTokens += resp.InputTokens
		res.OutputTokens += resp.OutputTokens
		switch {
		case resp.Stop == StopRefusal:
			res.Refused, res.RefusalCategory = true, resp.RefusalCategory
			return res, nil
		case len(resp.ToolCalls) == 0:
			return res, nil
		case resp.Stop == StopMaxTokens:
			return res, ErrTruncated
		}
		results := make([]ToolResult, 0, len(resp.ToolCalls))
		for _, c := range resp.ToolCalls {
			if h.OnTool != nil {
				h.OnTool(c)
			}
			t, ok := byName[c.Name]
			if !ok {
				results = append(results, ToolResult{ID: c.ID, Content: "unknown tool " + c.Name, IsError: true})
				continue
			}
			out, err := t.Run(ctx, c.Input)
			if err != nil {
				results = append(results, ToolResult{ID: c.ID, Content: err.Error(), IsError: true})
				continue
			}
			results = append(results, ToolResult{ID: c.ID, Content: out})
		}
		conv.AddToolResults(results)
	}
	return res, fmt.Errorf("stopped after %d steps without a final answer", maxSteps)
}

// DecodeInput parses a tool input strictly and checks required fields: with eager input
// streaming the API no longer validates tool inputs, so the client must.
func DecodeInput(raw json.RawMessage, v any, required ...string) error {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("invalid tool input: %w", err)
	}
	for _, f := range required {
		if x, ok := m[f]; !ok || x == nil || x == "" {
			return fmt.Errorf("missing required field %q", f)
		}
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("invalid tool input: %w", err)
	}
	return nil
}
