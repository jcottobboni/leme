// Package traffic reads observed connections between workloads from optional sources (Caretta or
// Hubble metrics in the configured Prometheus). Nothing is installed in the cluster (M5, feature traffic).
//
// The JSON shape below is the contract with the UI (app/renderer/src/features/map-webgl/trafficEdges.ts):
// change both together or not at all.
package traffic

// Endpoint is one side of a connection at workload level.
type Endpoint struct {
	Kind      string `json:"kind"` // controller kind, "Node" or "External"
	Group     string `json:"group,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// Edge is a directed, aggregated connection from client to server.
type Edge struct {
	From     Endpoint `json:"from"`
	To       Endpoint `json:"to"`
	Port     int      `json:"port,omitempty"`
	Protocol string   `json:"protocol,omitempty"`
	Rate     float64  `json:"rate"`
	Unit     string   `json:"unit"`   // "bytes/s" | "flows/s"
	Source   string   `json:"source"` // "caretta" | "hubble"
}

// Snapshot is the response of GET /api/clusters/{id}/traffic.
type Snapshot struct {
	Available bool              `json:"available"`
	Source    string            `json:"source,omitempty"`
	Edges     []Edge            `json:"edges"`
	Window    string            `json:"window,omitempty"`
	Hint      string            `json:"hint,omitempty"`
	Errors    map[string]string `json:"errors,omitempty"`
}
