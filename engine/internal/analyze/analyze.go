// Package analyze diagnoses common problems from object status, events and relations, without any
// AI: every finding carries the evidence it was derived from and a suggestion. It is the first
// opinion the Copilot and MCP agents build on, and works offline for everyone.
package analyze

import (
	"context"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// Severity of a finding.
const (
	Critical = "critical"
	High     = "high"
	Medium   = "medium"
	Low      = "low"
	Info     = "info"
)

// Ref identifies the affected object.
type Ref struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// Finding is one diagnosed problem.
type Finding struct {
	ID          string   `json:"id"`       // rule id, e.g. "image-pull"
	Severity    string   `json:"severity"` // critical | high | medium | low | info
	Object      Ref      `json:"object"`
	Title       string   `json:"title"`       // short, Portuguese (UI language)
	Explanation string   `json:"explanation"` // what is wrong and why it matters
	Evidence    []string `json:"evidence"`    // status fields / event messages it was derived from
	Suggestion  string   `json:"suggestion"`  // what to do
	Fixable     bool     `json:"fixable"`     // a patch to the object itself can fix it (Copilot can propose)
}

// Object diagnoses one object and what directly belongs to it (e.g. a Deployment and its pods).
func Object(ctx context.Context, s *cluster.Session, ref Ref) ([]Finding, error) {
	return []Finding{}, nil
}

// Namespace diagnoses every object of a namespace ("" = whole cluster).
func Namespace(ctx context.Context, s *cluster.Session, namespace string) ([]Finding, error) {
	return []Finding{}, nil
}
