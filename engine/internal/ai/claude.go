package ai

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

// claude is a conversation on the Messages API through the official SDK (beta namespace: needed
// for server-side refusal fallbacks).
type claude struct {
	client anthropic.Client
	params anthropic.BetaMessageNewParams
}

func newClaude(cfg Config, system string, history []Turn, user string, tools []ToolDef) *claude {
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey), option.WithMaxRetries(2)}
	official := cfg.BaseURL == ""
	if !official {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	p := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(cfg.Model),
		MaxTokens: cfg.MaxTokens,
		// Stable system prompt and tool list first, cached; the volatile context goes in messages.
		System: []anthropic.BetaTextBlockParam{{Text: system, CacheControl: anthropic.NewBetaCacheControlEphemeralParam()}},
	}
	if cfg.Effort != "" {
		p.OutputConfig = anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(cfg.Effort)}
	}
	if official {
		// A policy decline is re-served by Anthropic's recommended fallback model in the same call.
		// Only on the official endpoint: proxies and gateways may reject the field.
		p.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
		p.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
	}
	for _, t := range tools {
		tp := &anthropic.BetaToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: t.Schema["properties"], Required: requiredOf(t.Schema)},
			// Inputs stream as generated; DecodeInput validates them before any tool runs.
			EagerInputStreaming: anthropic.Bool(true),
		}
		p.Tools = append(p.Tools, anthropic.BetaToolUnionParam{OfTool: tp})
	}
	for _, h := range history {
		if h.Role == "assistant" {
			p.Messages = append(p.Messages, anthropic.BetaMessageParam{Role: anthropic.BetaMessageParamRoleAssistant, Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(h.Text)}})
		} else {
			p.Messages = append(p.Messages, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(h.Text)))
		}
	}
	p.Messages = append(p.Messages, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(user)))
	return &claude{client: anthropic.NewClient(opts...), params: p}
}

func requiredOf(schema map[string]any) []string {
	switch r := schema["required"].(type) {
	case []string:
		return r
	case []any:
		out := make([]string, 0, len(r))
		for _, x := range r {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func (c *claude) Step(ctx context.Context, onText func(string)) (*Response, error) {
	stream := c.client.Beta.Messages.NewStreaming(ctx, c.params)
	msg := anthropic.BetaMessage{}
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return nil, err
		}
		if d, ok := ev.AsAny().(anthropic.BetaRawContentBlockDeltaEvent); ok {
			if t, ok := d.Delta.AsAny().(anthropic.BetaTextDelta); ok {
				onText(t.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	// Keep the assistant turn exactly as produced (thinking blocks included) for the next step.
	c.params.Messages = append(c.params.Messages, msg.ToParam())
	r := &Response{InputTokens: msg.Usage.InputTokens, OutputTokens: msg.Usage.OutputTokens}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		r.Stop, r.RefusalCategory = StopRefusal, string(msg.StopDetails.Category)
		return r, nil
	case anthropic.BetaStopReasonMaxTokens:
		r.Stop = StopMaxTokens
	case anthropic.BetaStopReasonToolUse:
		r.Stop = StopToolUse
	default:
		r.Stop = StopEnd
	}
	for _, b := range msg.Content {
		switch v := b.AsAny().(type) {
		case anthropic.BetaTextBlock:
			r.Text += v.Text
		case anthropic.BetaToolUseBlock:
			r.ToolCalls = append(r.ToolCalls, ToolCall{ID: v.ID, Name: v.Name, Input: json.RawMessage(v.JSON.Input.Raw())})
		}
	}
	return r, nil
}

func (c *claude) AddToolResults(results []ToolResult) {
	blocks := make([]anthropic.BetaContentBlockParamUnion, 0, len(results))
	for _, r := range results {
		blocks = append(blocks, anthropic.NewBetaToolResultBlock(r.ID, r.Content, r.IsError))
	}
	// All results of a step in one user message (keeps parallel tool use working).
	c.params.Messages = append(c.params.Messages, anthropic.NewBetaUserMessage(blocks...))
}
