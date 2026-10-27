package traffic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/prom"
	"github.com/jcottobboni/leme/engine/internal/settings"
)

type series struct {
	metric map[string]string
	value  float64
}

// fakeProm answers /api/v1/query_range with the first rule whose substring is in the query.
type fakeProm struct {
	mu      sync.Mutex
	rules   []rule
	queries []string
	status  int
}

type rule struct {
	contains string
	result   []series
}

func (f *fakeProm) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("query")
	f.mu.Lock()
	f.queries = append(f.queries, q)
	status := f.status
	f.mu.Unlock()
	if r.URL.Path != "/api/v1/query_range" {
		http.NotFound(w, r)
		return
	}
	if status != 0 {
		http.Error(w, "boom", status)
		return
	}
	var res []series
	for _, ru := range f.rules {
		if strings.Contains(q, ru.contains) {
			res = ru.result
			break
		}
	}
	ts := r.URL.Query().Get("end")
	out := []map[string]any{}
	for _, s := range res {
		out = append(out, map[string]any{"metric": s.metric, "values": [][2]any{{json.Number(ts), fmt.Sprint(s.value)}}})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": out}})
}

func client(t *testing.T, f *fakeProm) *prom.Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := prom.NewClient(&cluster.Session{Config: &rest.Config{}}, settings.Prometheus{Mode: "url", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func names(m ...string) []series {
	var out []series
	for _, n := range m {
		out = append(out, series{metric: map[string]string{"__name__": n}, value: 1})
	}
	return out
}

func edge(v float64, parts ...string) series {
	return series{metric: map[string]string{legendLabel: strings.Join(parts, sep)}, value: v}
}

func TestCaretta(t *testing.T) {
	f := &fakeProm{rules: []rule{
		{contains: "count by (__name__)", result: names(metricCaretta, metricHubble)},
		{contains: "caretta_links_observed", result: []series{
			edge(120, "Deployment", "demo", "web", "Deployment", "demo", "m5-traffic-backend", "80"),
			edge(5, "deployment", "demo", "web", "external", "", "10.0.0.9", "443"),
			edge(0, "Deployment", "demo", "idle", "Deployment", "demo", "db", "5432"),
			edge(7, "DaemonSet", "kube-system", "agent", "node", "", "kind-control-plane", "10250"),
			edge(1, "bad"),
		}},
	}}
	snap := Collect(context.Background(), client(t, f), Options{})
	if !snap.Available || snap.Source != SourceCaretta || snap.Window != "5m" || snap.Hint != "" || snap.Errors != nil {
		t.Fatalf("snapshot: %+v", snap)
	}
	if len(snap.Edges) != 3 {
		t.Fatalf("idle and malformed links must be dropped: %+v", snap.Edges)
	}
	e := snap.Edges[0]
	want := Edge{From: Endpoint{Kind: "Deployment", Group: "apps", Namespace: "demo", Name: "web"}, To: Endpoint{Kind: "Deployment", Group: "apps", Namespace: "demo", Name: "m5-traffic-backend"}, Port: 80, Protocol: "TCP", Rate: 120, Unit: UnitBytes, Source: SourceCaretta}
	if e != want {
		t.Fatalf("got %+v\nwant %+v", e, want)
	}
	if snap.Edges[1].To.Kind != "Node" || snap.Edges[1].To.Namespace != "" || snap.Edges[2].To.Kind != "External" || snap.Edges[2].From.Kind != "Deployment" {
		t.Fatalf("kind normalization: %+v", snap.Edges[1:])
	}
	var q string
	for _, x := range f.queries {
		if strings.Contains(x, "increase(") {
			q = x
		}
	}
	wantQ := `label_join(sum by (client_kind,client_namespace,client_name,server_kind,server_namespace,server_name,server_port) (increase(caretta_links_observed[5m])) / 300, "leme_edge", "|", "client_kind", "client_namespace", "client_name", "server_kind", "server_namespace", "server_name", "server_port")`
	if q != wantQ {
		t.Fatalf("query\n got %s\nwant %s", q, wantQ)
	}
}

func TestHubble(t *testing.T) {
	f := &fakeProm{rules: []rule{
		{contains: "count by (__name__)", result: names(metricHubble)},
		{contains: "label_join", result: []series{
			edge(2.5, "demo", "web", "Deployment", "demo", "api", "", "TCP"),
			edge(0.5, "demo", "web", "", "kube-system", "coredns", "", "UDP"),
		}},
	}}
	snap := Collect(context.Background(), client(t, f), Options{Window: "15m"})
	if !snap.Available || snap.Source != SourceHubble || snap.Window != "15m" || len(snap.Edges) != 2 {
		t.Fatalf("snapshot: %+v", snap)
	}
	e := snap.Edges[1]
	if e.Unit != UnitFlows || e.To.Kind != "Deployment" || e.To.Namespace != "kube-system" || e.Protocol != "UDP" || e.Port != 0 {
		t.Fatalf("hubble edge: %+v", e)
	}
	if !strings.Contains(strings.Join(f.queries, "\n"), `rate(hubble_flows_processed_total{source_workload!="",destination_workload!=""}[15m])`) {
		t.Fatalf("queries: %v", f.queries)
	}
}

func TestHubbleWithoutLabelsContext(t *testing.T) {
	f := &fakeProm{rules: []rule{
		{contains: "count by (__name__)", result: names(metricHubble)},
		{contains: "label_join", result: nil},
		{contains: `count(hubble_flows_processed_total{source_workload!=""})`, result: nil},
	}}
	snap := Collect(context.Background(), client(t, f), Options{})
	if snap.Available || snap.Source != SourceHubble || snap.Hint != HintHubbleLabels || len(snap.Edges) != 0 || snap.Edges == nil {
		t.Fatalf("snapshot: %+v", snap)
	}
}

func TestNoSource(t *testing.T) {
	f := &fakeProm{rules: []rule{{contains: "count by (__name__)", result: nil}}}
	snap := Collect(context.Background(), client(t, f), Options{})
	if snap.Available || snap.Source != "" || snap.Hint != HintNoSource || snap.Edges == nil || len(f.queries) != 1 {
		t.Fatalf("snapshot: %+v queries %v", snap, f.queries)
	}
	b, _ := json.Marshal(snap)
	if !strings.Contains(string(b), `"edges":[]`) {
		t.Fatalf("edges must serialize as an array: %s", b)
	}
}

func TestPrometheusError(t *testing.T) {
	f := &fakeProm{status: http.StatusBadGateway}
	snap := Collect(context.Background(), client(t, f), Options{})
	if snap.Available || snap.Errors["prometheus"] == "" || snap.Hint == "" {
		t.Fatalf("snapshot: %+v", snap)
	}
}

func TestCarettaQueryError(t *testing.T) {
	f := &fakeProm{rules: []rule{{contains: "count by (__name__)", result: names(metricCaretta)}}}
	q := &failing{Querier: client(t, f), when: "increase("}
	snap := Collect(context.Background(), q, Options{})
	if snap.Available || snap.Source != SourceCaretta || snap.Errors[SourceCaretta] == "" {
		t.Fatalf("partial error: %+v", snap)
	}
}

type failing struct {
	Querier
	when string
}

func (f *failing) QueryRange(ctx context.Context, expr string, s, e time.Time, step time.Duration, legend string) ([]prom.Series, error) {
	if strings.Contains(expr, f.when) {
		return nil, fmt.Errorf("query failed")
	}
	return f.Querier.QueryRange(ctx, expr, s, e, step, legend)
}

func TestNamespaceFilter(t *testing.T) {
	f := &fakeProm{rules: []rule{
		{contains: "count by (__name__)", result: names(metricCaretta)},
		{contains: "increase(", result: []series{
			edge(10, "Deployment", "demo", "web", "Deployment", "demo", "api", "80"),
			edge(9, "Deployment", "other", "x", "Deployment", "demo", "api", "80"),
			edge(8, "Deployment", "demo", "web", "Deployment", "other", "y", "80"),
			edge(7, "Deployment", "other", "x", "Deployment", "other", "y", "80"),   // dropped: neither end in demo
			edge(3, "Deployment", "demo", "web", "Deployment", "demo", "api", "80"), // merged with the first
		}},
	}}
	snap := Collect(context.Background(), client(t, f), Options{Namespace: "demo"})
	if len(snap.Edges) != 3 || snap.Edges[0].Rate != 13 {
		t.Fatalf("filter: %+v", snap.Edges)
	}
	var q string
	for _, x := range f.queries {
		if strings.Contains(x, "increase(") {
			q = x
		}
	}
	if !strings.Contains(q, `increase(caretta_links_observed{client_namespace="demo"}[5m]) or increase(caretta_links_observed{server_namespace="demo"}[5m])`) {
		t.Fatalf("query: %s", q)
	}
	if bad := Collect(context.Background(), client(t, f), Options{Namespace: `x"} or vector(1)`}); bad.Errors["namespace"] == "" || bad.Available {
		t.Fatalf("invalid namespace must be rejected: %+v", bad)
	}
	if bad := Collect(context.Background(), client(t, f), Options{Window: "2d"}); bad.Errors["window"] == "" {
		t.Fatalf("invalid window must be rejected: %+v", bad)
	}
}

func TestCap(t *testing.T) {
	var many []series
	for i := 0; i < MaxEdges+50; i++ {
		many = append(many, edge(float64(i+1), "Deployment", "demo", fmt.Sprintf("c%d", i), "Deployment", "demo", "srv", "80"))
	}
	f := &fakeProm{rules: []rule{
		{contains: "count by (__name__)", result: names(metricCaretta)},
		{contains: "increase(", result: many},
	}}
	snap := Collect(context.Background(), client(t, f), Options{})
	if len(snap.Edges) != MaxEdges || snap.Errors["limit"] == "" {
		t.Fatalf("cap: %d %+v", len(snap.Edges), snap.Errors)
	}
	if snap.Edges[0].Rate != float64(MaxEdges+50) || snap.Edges[MaxEdges-1].Rate != 51 {
		t.Fatalf("the highest-volume edges must be kept: first %v last %v", snap.Edges[0].Rate, snap.Edges[MaxEdges-1].Rate)
	}
}
