package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// OllamaBaseURL is the OpenAI-compatible endpoint of a local Ollama (M7 RF07).
const OllamaBaseURL = "http://127.0.0.1:11434/v1"

// IsLoopback reports whether an endpoint URL points at this machine: 127.0.0.0/8, ::1 (also
// IPv4-mapped) or the name localhost. Only these count as local models: nothing leaves the
// computer, so there is no cloud consent. Look-alike names (127.0.0.1.example.com,
// localhost.evil) are not loopback; names are never resolved, so DNS cannot change the answer.
func IsLoopback(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" {
		return false
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if h == "localhost" {
		return true
	}
	if i := strings.IndexByte(h, '%'); i >= 0 { // IPv6 zone
		h = h[:i]
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// IsLocal reports whether a provider configuration runs on this machine. An empty base URL is
// the official endpoint: local only for Ollama (whose default is OllamaBaseURL).
func IsLocal(provider, baseURL string) bool {
	if strings.TrimSpace(baseURL) == "" {
		return provider == Ollama
	}
	return IsLoopback(baseURL)
}

// LocalModel is a model installed in a local Ollama.
type LocalModel struct {
	Name     string `json:"name"`
	Size     int64  `json:"size,omitempty"`
	Modified string `json:"modified,omitempty"`
	Family   string `json:"family,omitempty"`
	Params   string `json:"parameters,omitempty"`
}

// ErrNotLocal refuses listing models of an endpoint that is not on this machine.
var ErrNotLocal = errors.New("a lista de modelos só é lida de um Ollama local (127.0.0.1, ::1 ou localhost)")

// ollamaRoot turns the OpenAI-compatible base (http://host:11434/v1) into the Ollama API root.
func ollamaRoot(baseURL string) string {
	b := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if b == "" {
		b = OllamaBaseURL
	}
	return strings.TrimSuffix(b, "/v1")
}

// ListOllamaModels reads GET /api/tags of a local Ollama. Only loopback endpoints are queried.
func ListOllamaModels(ctx context.Context, baseURL string) ([]LocalModel, error) {
	root := ollamaRoot(baseURL)
	if !IsLoopback(root) {
		return nil, ErrNotLocal
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	// No redirects: a local server must not bounce the request somewhere else.
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("o Ollama não respondeu em %s: %w", root, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("o Ollama respondeu %s em %s/api/tags", res.Status, root)
	}
	var body struct {
		Models []struct {
			Name       string `json:"name"`
			Model      string `json:"model"`
			Size       int64  `json:"size"`
			ModifiedAt string `json:"modified_at"`
			Details    struct {
				Family        string `json:"family"`
				ParameterSize string `json:"parameter_size"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("resposta inesperada de %s/api/tags: %w", root, err)
	}
	out := make([]LocalModel, 0, len(body.Models))
	for _, m := range body.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name == "" || len(out) >= 500 {
			continue
		}
		out = append(out, LocalModel{Name: name, Size: m.Size, Modified: m.ModifiedAt, Family: m.Details.Family, Params: m.Details.ParameterSize})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
