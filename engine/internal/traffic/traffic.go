package traffic

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jcottobboni/leme/engine/internal/prom"
)

// Querier is the part of prom.Client the collector needs (a range query over a single instant keeps
// the series labels reachable through the legend).
type Querier interface {
	QueryRange(ctx context.Context, expr string, start, end time.Time, step time.Duration, legend string) ([]prom.Series, error)
}

// MaxEdges caps a snapshot; the edges with the highest volume are kept.
const MaxEdges = 2000

// Source names and units (JSON contract).
const (
	SourceCaretta = "caretta"
	SourceHubble  = "hubble"
	UnitBytes     = "bytes/s"
	UnitFlows     = "flows/s"
)

// Windows are the accepted observation windows.
var Windows = map[string]time.Duration{"1m": time.Minute, "5m": 5 * time.Minute, "15m": 15 * time.Minute, "30m": 30 * time.Minute, "1h": time.Hour}

// DefaultWindow is used when the request does not name one.
const DefaultWindow = "5m"

// Hints shown when no source is available (Portuguese: they reach the UI verbatim).
const (
	HintNoPrometheus = "Configure o Prometheus deste cluster (painel Métricas → Prometheus) para ler o tráfego observado. O Leme não instala nada no cluster."
	HintNoSource     = "Nenhuma fonte de tráfego no Prometheus. Opções: (1) Caretta — helm repo add groundcover https://helm.groundcover.com && helm install caretta groundcover/caretta, e faça o Prometheus raspar a métrica caretta_links_observed; (2) Hubble (Cilium) — habilite a métrica flow com labelsContext=source_namespace,source_workload,destination_namespace,destination_workload. O Leme não instala nada no cluster."
	HintHubbleLabels = "O Hubble está exportando hubble_flows_processed_total, mas sem os rótulos de workload. Habilite a métrica flow com labelsContext=source_namespace,source_workload,destination_namespace,destination_workload."
)

// Options selects what to collect.
type Options struct {
	Namespace string // either end must be in this namespace; empty = all
	Window    string // one of Windows; empty = DefaultWindow
	Now       time.Time
}

const (
	metricCaretta = "caretta_links_observed"
	metricHubble  = "hubble_flows_processed_total"
	legendLabel   = "leme_edge"
	sep           = "|"
)

var (
	carettaLabels = []string{"client_kind", "client_namespace", "client_name", "server_kind", "server_namespace", "server_name", "server_port"}
	hubbleLabels  = []string{"source_namespace", "source_workload", "source_workload_kind", "destination_namespace", "destination_workload", "destination_workload_kind", "protocol"}
	dns1123       = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// ValidNamespace reports whether ns can be used in a PromQL matcher as is.
func ValidNamespace(ns string) bool { return ns == "" || (len(ns) <= 63 && dns1123.MatchString(ns)) }

// Collect detects the available source and returns its edges. Failures are reported in Errors; the
// snapshot is always usable (available=false with a hint when nothing can be read).
func Collect(ctx context.Context, q Querier, opt Options) (snap Snapshot) {
	if opt.Window == "" {
		opt.Window = DefaultWindow
	}
	win, ok := Windows[opt.Window]
	snap = Snapshot{Edges: []Edge{}, Window: opt.Window}
	if !ok {
		snap.Errors = map[string]string{"window": "janela inválida: " + opt.Window}
		return snap
	}
	if !ValidNamespace(opt.Namespace) {
		snap.Errors = map[string]string{"namespace": "namespace inválido"}
		return snap
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	errs := map[string]string{}
	defer func() { // named result: errors recorded after any return still reach the caller
		if len(errs) > 0 {
			snap.Errors = errs
		}
	}()

	present, err := probe(ctx, q, opt.Now)
	if err != nil {
		errs["prometheus"] = err.Error()
		snap.Hint = HintNoSource
		return snap
	}
	var edges []Edge
	switch {
	case present[metricCaretta]: // preferred over Hubble when both exist: it measures bytes, not flows
		snap.Source = SourceCaretta
		edges, err = queryEdges(ctx, q, opt, carettaExpr(opt.Namespace, opt.Window, win), parseCaretta)
		if err != nil {
			errs[SourceCaretta] = err.Error()
		}
		snap.Available = err == nil
	case present[metricHubble]:
		snap.Source = SourceHubble
		edges, err = queryEdges(ctx, q, opt, hubbleExpr(opt.Namespace, opt.Window), parseHubble)
		if err != nil {
			errs[SourceHubble] = err.Error()
			break
		}
		if len(edges) == 0 {
			// Edges need the workload labels; without labelsContext the metric exists but cannot be used.
			labeled, err := hasSeries(ctx, q, opt.Now, `count(`+metricHubble+`{source_workload!=""})`)
			if err != nil {
				errs[SourceHubble] = err.Error()
				break
			}
			if !labeled {
				snap.Hint = HintHubbleLabels
				break
			}
		}
		snap.Available = true
	default:
		snap.Hint = HintNoSource
		return snap
	}
	edges = merge(edges, opt.Namespace)
	if len(edges) > MaxEdges {
		errs["limit"] = fmt.Sprintf("%d conexões observadas; mostrando as %d de maior volume.", len(edges), MaxEdges)
		edges = edges[:MaxEdges]
	}
	snap.Edges = edges
	return snap
}

// probe returns which of the known metrics exist in Prometheus.
func probe(ctx context.Context, q Querier, now time.Time) (map[string]bool, error) {
	expr := `count by (__name__) ({__name__=~"` + metricCaretta + `|` + metricHubble + `"})`
	series, err := q.QueryRange(ctx, expr, now, now, time.Minute, "__name__")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, s := range series {
		if len(s.Points) > 0 {
			out[s.Name] = true
		}
	}
	return out, nil
}

func hasSeries(ctx context.Context, q Querier, now time.Time, expr string) (bool, error) {
	series, err := q.QueryRange(ctx, expr, now, now, time.Minute, "")
	if err != nil {
		return false, err
	}
	for _, s := range series {
		if len(s.Points) > 0 && s.Points[len(s.Points)-1][1] > 0 {
			return true, nil
		}
	}
	return false, nil
}

// selectorOr applies the namespace filter on either end: (f(a) or f(b)).
func selectorOr(fn func(sel string) string, nsLabels [2]string, ns string) string {
	if ns == "" {
		return fn("")
	}
	return fn(`{`+nsLabels[0]+`="`+ns+`"}`) + " or " + fn(`{`+nsLabels[1]+`="`+ns+`"}`)
}

func joined(expr string, labels []string) string {
	return `label_join(` + expr + `, "` + legendLabel + `", "` + sep + `", "` + strings.Join(labels, `", "`) + `")`
}

func carettaExpr(ns, window string, win time.Duration) string {
	inner := selectorOr(func(sel string) string { return `increase(` + metricCaretta + sel + `[` + window + `])` }, [2]string{"client_namespace", "server_namespace"}, ns)
	return joined(`sum by (`+strings.Join(carettaLabels, ",")+`) (`+inner+`) / `+strconv.Itoa(int(win.Seconds())), carettaLabels)
}

func hubbleExpr(ns, window string) string {
	inner := selectorOr(func(sel string) string {
		m := `source_workload!="",destination_workload!=""`
		if sel != "" {
			m += "," + strings.Trim(sel, "{}")
		}
		return `rate(` + metricHubble + `{` + m + `}[` + window + `])`
	}, [2]string{"source_namespace", "destination_namespace"}, ns)
	return joined(`sum by (`+strings.Join(hubbleLabels, ",")+`) (`+inner+`)`, hubbleLabels)
}

func queryEdges(ctx context.Context, q Querier, opt Options, expr string, parse func([]string, float64) (Edge, bool)) ([]Edge, error) {
	series, err := q.QueryRange(ctx, expr, opt.Now, opt.Now, time.Minute, legendLabel)
	if err != nil {
		return nil, err
	}
	var out []Edge
	for _, s := range series {
		if len(s.Points) == 0 {
			continue
		}
		parts := strings.Split(s.Name, sep)
		if len(parts) != 7 {
			continue
		}
		if e, ok := parse(parts, s.Points[len(s.Points)-1][1]); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

func parseCaretta(p []string, v float64) (Edge, bool) {
	from := endpoint(p[0], p[1], p[2])
	to := endpoint(p[3], p[4], p[5])
	if from.Name == "" || to.Name == "" {
		return Edge{}, false
	}
	port, _ := strconv.Atoi(p[6])
	return Edge{From: from, To: to, Port: port, Protocol: "TCP", Rate: v, Unit: UnitBytes, Source: SourceCaretta}, true
}

func parseHubble(p []string, v float64) (Edge, bool) {
	kind := func(k string) string {
		if k == "" {
			return "Deployment" // older Cilium versions do not export the workload kind
		}
		return k
	}
	from := endpoint(kind(p[2]), p[0], p[1])
	to := endpoint(kind(p[5]), p[3], p[4])
	if from.Name == "" || to.Name == "" {
		return Edge{}, false
	}
	return Edge{From: from, To: to, Protocol: p[6], Rate: v, Unit: UnitFlows, Source: SourceHubble}, true
}

var groups = map[string]string{
	"Deployment": "apps", "StatefulSet": "apps", "DaemonSet": "apps", "ReplicaSet": "apps",
	"Job": "batch", "CronJob": "batch", "DeploymentConfig": "apps.openshift.io",
}

// kinds maps lower-case kinds reported by the sources to the Kubernetes spelling.
var kinds = map[string]string{
	"": "External", "external": "External", "externalip": "External", "world": "External", "unknown": "External",
	"node": "Node", "pod": "Pod", "deployment": "Deployment", "statefulset": "StatefulSet", "daemonset": "DaemonSet",
	"replicaset": "ReplicaSet", "job": "Job", "cronjob": "CronJob", "deploymentconfig": "DeploymentConfig",
}

// endpoint normalizes source-specific kinds ("deployment", "external", "node") to the contract.
func endpoint(kind, ns, name string) Endpoint {
	k, ok := kinds[strings.ToLower(kind)]
	if !ok {
		k = kind
	}
	e := Endpoint{Kind: k, Group: groups[k], Namespace: ns, Name: name}
	if k == "External" || k == "Node" {
		e.Namespace = ""
	}
	return e
}

type edgeKey struct {
	from, to Endpoint
	port     int
	protocol string
}

// merge applies the namespace filter (defensive: the query already filters), sums duplicates,
// drops idle links and sorts by volume, highest first.
func merge(in []Edge, ns string) []Edge {
	idx := map[edgeKey]int{}
	out := make([]Edge, 0, len(in))
	for _, e := range in {
		if ns != "" && e.From.Namespace != ns && e.To.Namespace != ns {
			continue
		}
		if !(e.Rate > 0) {
			continue
		}
		k := edgeKey{e.From, e.To, e.Port, e.Protocol}
		if i, ok := idx[k]; ok {
			out[i].Rate += e.Rate
			continue
		}
		idx[k] = len(out)
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rate != out[j].Rate {
			return out[i].Rate > out[j].Rate
		}
		a, b := out[i], out[j]
		return a.From.Namespace+"/"+a.From.Name+">"+a.To.Namespace+"/"+a.To.Name < b.From.Namespace+"/"+b.From.Name+">"+b.To.Namespace+"/"+b.To.Name
	})
	return out
}
