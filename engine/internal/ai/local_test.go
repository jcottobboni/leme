package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsLoopback(t *testing.T) {
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"http://127.0.0.1:11434/v1", true},
		{"http://127.1.2.3:8080", true},
		{"http://localhost:11434/v1", true},
		{"http://LOCALHOST.:1234", true},
		{"http://[::1]:11434/v1", true},
		{"http://[::ffff:127.0.0.1]:11434", true},
		{"https://api.openai.com/v1", false},
		{"http://127.0.0.1.nip.io/v1", false},
		{"http://localhost.evil.com/v1", false},
		{"http://10.0.0.5:11434/v1", false},
		{"http://[::2]:11434", false},
		{"http://0.0.0.0:11434", false},
		{"127.0.0.1:11434", false}, // no scheme: not a URL with a host
		{"", false},
		{"::not a url", false},
	} {
		if got := IsLoopback(c.url); got != c.want {
			t.Errorf("IsLoopback(%q) = %v, want %v", c.url, got, c.want)
		}
	}
	if !IsLocal(Ollama, "") || IsLocal(OpenAI, "") || IsLocal(Anthropic, "") {
		t.Error("empty base URL is local only for Ollama")
	}
	if IsLocal(Ollama, "https://ollama.example.com/v1") {
		t.Error("a remote Ollama is not local")
	}
	if !IsLocal(OpenAI, "http://127.0.0.1:1234/v1") {
		t.Error("LM Studio on loopback is local")
	}
}

func TestListOllamaModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5:7b","size":4700000000,"modified_at":"2026-10-01T00:00:00Z","details":{"family":"qwen2","parameter_size":"7.6B"}},{"model":"llama3.1:8b"}]}`))
	}))
	defer srv.Close()
	ms, err := ListOllamaModels(context.Background(), srv.URL+"/v1/")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Name != "llama3.1:8b" || ms[1].Name != "qwen2.5:7b" || ms[1].Params != "7.6B" {
		t.Fatalf("%+v", ms)
	}
	if _, err := ListOllamaModels(context.Background(), "https://ollama.example.com/v1"); !errors.Is(err, ErrNotLocal) {
		t.Fatalf("remote endpoint must be refused, got %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", 500) }))
	defer bad.Close()
	if _, err := ListOllamaModels(context.Background(), bad.URL); err == nil {
		t.Fatal("server error must fail")
	}
	if ollamaRoot("") != "http://127.0.0.1:11434" {
		t.Fatal(ollamaRoot(""))
	}
}
