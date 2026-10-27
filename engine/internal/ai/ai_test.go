package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// sse writes Anthropic stream events.
func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		var m map[string]any
		_ = json.Unmarshal([]byte(e), &m)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m["type"], e)
	}
}

const msgStart = `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1}}}`

func textBlock(i int, text string) []string {
	b, _ := json.Marshal(text)
	return []string{
		fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, i),
		fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`, i, b),
		fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i),
	}
}

func toolBlock(i int, id, name, input string) []string {
	b, _ := json.Marshal(input)
	return []string{
		fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":%q,"name":%q,"input":{}}}`, i, id, name),
		fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%s}}`, i, b),
		fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i),
	}
}

func stop(reason, extra string) []string {
	return []string{fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q%s},"usage":{"output_tokens":30}}`, reason, extra), `{"type":"message_stop"}`}
}

type fakeClaude struct {
	mu       sync.Mutex
	requests []map[string]any
	headers  []http.Header
	steps    [][]string
}

func (f *fakeClaude) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	f.mu.Lock()
	n := len(f.requests)
	f.requests = append(f.requests, m)
	f.headers = append(f.headers, r.Header.Clone())
	f.mu.Unlock()
	if n >= len(f.steps) {
		http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"no more steps"}}`, 400)
		return
	}
	sse(w, f.steps[n]...)
}

var echoTool = Tool{
	Def: ToolDef{Name: "lookup", Description: "look up", Schema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []string{"name"}}},
	Run: func(ctx context.Context, in json.RawMessage) (string, error) {
		var v struct{ Name string }
		if err := DecodeInput(in, &v, "name"); err != nil {
			return "", err
		}
		return "found " + v.Name, nil
	},
}

func TestClaudeToolLoop(t *testing.T) {
	f := &fakeClaude{steps: [][]string{
		append(append(append([]string{msgStart}, textBlock(0, "Vou verificar. ")...), toolBlock(1, "tu_1", "lookup", `{"name":"web"}`)...), stop("tool_use", "")...),
		append(append([]string{msgStart}, textBlock(0, "Pronto: web encontrado.")...), stop("end_turn", "")...),
	}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	conv, err := Start(Config{Provider: Anthropic, APIKey: "k", BaseURL: srv.URL, Effort: "medium"}, "system prompt", []Turn{{Role: "user", Text: "oi"}, {Role: "assistant", Text: "olá"}}, "procure web", []ToolDef{echoTool.Def})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var calls []string
	res, err := Run(context.Background(), conv, []Tool{echoTool}, 5, Hooks{OnText: func(s string) { text.WriteString(s) }, OnTool: func(c ToolCall) { calls = append(calls, c.Name) }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps != 2 || text.String() != "Vou verificar. Pronto: web encontrado." || len(calls) != 1 {
		t.Fatalf("res=%+v text=%q calls=%v", res, text.String(), calls)
	}
	// Request shape: model, effort, cached system, eager tool streaming, no fallbacks off the official endpoint.
	first := f.requests[0]
	if first["model"] != "claude-opus-5-5" || first["output_config"].(map[string]any)["effort"] != "medium" {
		t.Errorf("model/effort: %v", first)
	}
	sys := first["system"].([]any)[0].(map[string]any)
	if sys["cache_control"] == nil {
		t.Error("system prompt must be cached")
	}
	tool := first["tools"].([]any)[0].(map[string]any)
	if tool["eager_input_streaming"] != true {
		t.Error("tools must stream input eagerly")
	}
	if _, ok := first["fallbacks"]; ok {
		t.Error("no fallbacks on a custom endpoint")
	}
	// Second request carries the assistant tool_use and the tool_result.
	msgs := f.requests[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	block := last["content"].([]any)[0].(map[string]any)
	if block["type"] != "tool_result" || block["tool_use_id"] != "tu_1" {
		t.Fatalf("tool result: %v", last)
	}
}

func TestClaudeRefusalAndTruncation(t *testing.T) {
	f := &fakeClaude{steps: [][]string{append(append([]string{msgStart}, textBlock(0, "")...), stop("refusal", `,"stop_details":{"type":"refusal","category":"cyber","explanation":"x"}`)...)}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	conv, _ := Start(Config{Provider: Anthropic, APIKey: "k", BaseURL: srv.URL}, "s", nil, "q", nil)
	res, err := Run(context.Background(), conv, nil, 3, Hooks{})
	if err != nil || !res.Refused || res.RefusalCategory != "cyber" {
		t.Fatalf("refusal: %+v %v", res, err)
	}

	ran := false
	tr := Tool{Def: echoTool.Def, Run: func(context.Context, json.RawMessage) (string, error) { ran = true; return "", nil }}
	f2 := &fakeClaude{steps: [][]string{append(append([]string{msgStart}, toolBlock(0, "tu_1", "lookup", `{"name":"w`)...), stop("max_tokens", "")...)}}
	srv2 := httptest.NewServer(f2)
	defer srv2.Close()
	conv2, _ := Start(Config{Provider: Anthropic, APIKey: "k", BaseURL: srv2.URL}, "s", nil, "q", []ToolDef{tr.Def})
	if _, err := Run(context.Background(), conv2, []Tool{tr}, 3, Hooks{}); err != ErrTruncated || ran {
		t.Fatalf("truncated tool input must not run: err=%v ran=%v", err, ran)
	}
}

func TestOpenAICompatible(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		mu.Lock()
		n := len(bodies)
		bodies = append(bodies, m)
		mu.Unlock()
		if r.Header.Get("Authorization") != "" {
			t.Error("ollama without key must not send Authorization")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 0 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Checando\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"na\"}}]}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"me\\\":\\\"web\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	conv, err := Start(Config{Provider: Ollama, Model: "llama3.1", BaseURL: srv.URL}, "sys", nil, "q", []ToolDef{echoTool.Def})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	res, err := Run(context.Background(), conv, []Tool{echoTool}, 4, Hooks{OnText: func(s string) { text.WriteString(s) }})
	if err != nil || res.Steps != 2 || text.String() != "Checando ok" {
		t.Fatalf("res=%+v err=%v text=%q", res, err, text.String())
	}
	msgs := bodies[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" || last["tool_call_id"] != "c1" || last["content"] != "found web" {
		t.Fatalf("tool message: %v", last)
	}
}

func TestDecodeInput(t *testing.T) {
	var v struct{ Name string }
	if err := DecodeInput(json.RawMessage(`{"name":`), &v, "name"); err == nil {
		t.Error("truncated JSON must fail")
	}
	if err := DecodeInput(json.RawMessage(`{"other":1}`), &v, "name"); err == nil {
		t.Error("missing required must fail")
	}
}

func TestClaudeOfficialEndpointUsesFallbacks(t *testing.T) {
	c := newClaude(Config{Provider: Anthropic, APIKey: "k", Model: "claude-opus-5-5", MaxTokens: 1000}, "s", nil, "q", nil)
	b, _ := json.Marshal(c.params)
	if !strings.Contains(string(b), `"fallbacks":"default"`) {
		t.Fatalf("fallbacks missing: %s", b)
	}
	if len(c.params.Betas) != 1 || c.params.Betas[0] != "server-side-fallback-2026-07-01" {
		t.Fatalf("beta header: %v", c.params.Betas)
	}
}
