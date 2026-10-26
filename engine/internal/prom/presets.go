package prom

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Scope selects what a chart is about.
type Scope struct {
	Kind      string   `json:"kind"` // pod, workload, node, namespace, cluster
	Namespace string   `json:"namespace,omitempty"`
	Name      string   `json:"name,omitempty"`
	Pods      []string `json:"pods,omitempty"` // workload: current pod names
}

// Chart is the answer for one metric.
type Chart struct {
	Metric  string   `json:"metric"`
	Unit    string   `json:"unit"` // cores, bytes, bytes/s
	Series  []Series `json:"series"`
	Queries []string `json:"queries"`
	Step    int      `json:"step"`
}

// Ranges offered by the UI.
var Ranges = map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour}

// Steps returns the query step and rate window for a range (~200 points, never under 15s).
func Steps(rng time.Duration) (step, window time.Duration) {
	step = (rng / 200).Round(time.Second)
	if step < 15*time.Second {
		step = 15 * time.Second
	}
	window = 2 * time.Minute
	if step > window {
		window = step
	}
	return step, window
}

// q quotes a PromQL string literal (PromQL follows Go string literal escaping).
func q(s string) string { return strconv.Quote(s) }

var dnsName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

type query struct {
	expr   string
	legend string // label used as series name, or "=fixed name"
	ref    string // reference line (capacity, allocatable, requests, limits); optional, never fails the chart
}

// refQueries returns the optional reference lines of cpu/memory: machine capacity (cAdvisor) for node/cluster,
// allocatable and requests/limits from kube-state-metrics. Missing metrics simply yield no series.
func refQueries(sc Scope, metric, sel, nodeLabel string) []query {
	res, machine := "cpu", "machine_cpu_cores"
	if metric == "memory" {
		res, machine = "memory", "machine_memory_bytes"
	}
	braces := func(extra ...string) string {
		var parts []string
		for _, e := range extra {
			if e != "" {
				parts = append(parts, e)
			}
		}
		if len(parts) == 0 {
			return ""
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	var machSel, nodeSel, podSel string
	switch sc.Kind {
	case "node":
		machSel, nodeSel, podSel = fmt.Sprintf(`%s=%s`, nodeLabel, q(sc.Name)), fmt.Sprintf(`node=%s`, q(sc.Name)), fmt.Sprintf(`node=%s`, q(sc.Name))
	case "cluster":
	default: // pod, workload, namespace: only the pod-level reference lines make sense
		podSel = sel
	}
	var out []query
	if sc.Kind == "node" || sc.Kind == "cluster" {
		out = append(out,
			query{fmt.Sprintf(`sum(%s%s)`, machine, braces(machSel)), "=capacity", "capacity"},
			query{fmt.Sprintf(`sum(kube_node_status_allocatable%s)`, braces(fmt.Sprintf(`resource=%s`, q(res)), nodeSel)), "=allocatable", "allocatable"})
	}
	for _, r := range []string{"requests", "limits"} {
		out = append(out, query{fmt.Sprintf(`sum(kube_pod_container_resource_%s%s)`, r, braces(fmt.Sprintf(`resource=%s`, q(res)), podSel)), "=" + r, r})
	}
	return out
}

const ctr = `container!="",container!="POD"`

func durationLiteral(d time.Duration) string { return strconv.Itoa(int(d.Seconds())) + "s" }

// build returns the PromQL for a metric in a scope. nodeLabel is the label that holds the node name.
func build(sc Scope, metric string, window time.Duration, nodeLabel string) ([]query, string, error) {
	w := durationLiteral(window)
	var sel, by string
	switch sc.Kind {
	case "pod":
		sel, by = fmt.Sprintf(`namespace=%s,pod=%s`, q(sc.Namespace), q(sc.Name)), "container"
	case "workload":
		if len(sc.Pods) == 0 {
			return nil, "", errors.New("workload has no pods")
		}
		names := make([]string, 0, len(sc.Pods))
		for _, p := range sc.Pods {
			if !dnsName.MatchString(p) {
				return nil, "", fmt.Errorf("invalid pod name %q", p)
			}
			names = append(names, regexp.QuoteMeta(p))
		}
		sel, by = fmt.Sprintf(`namespace=%s,pod=~%s`, q(sc.Namespace), q(strings.Join(names, "|"))), "pod"
	case "namespace":
		sel, by = fmt.Sprintf(`namespace=%s`, q(sc.Namespace)), "pod"
	case "node":
		sel, by = fmt.Sprintf(`id="/",%s=%s`, nodeLabel, q(sc.Name)), ""
	case "cluster":
		sel, by = `id="/"`, nodeLabel
	default:
		return nil, "", fmt.Errorf("unknown scope %q", sc.Kind)
	}
	containerSel := sel
	if sc.Kind != "node" && sc.Kind != "cluster" {
		containerSel = sel + "," + ctr
	}
	agg := func(inner string) string {
		if by == "" {
			return "sum(" + inner + ")"
		}
		if sc.Kind == "namespace" {
			return "topk(10, sum by (" + by + ") (" + inner + "))"
		}
		return "sum by (" + by + ") (" + inner + ")"
	}
	switch metric {
	case "cpu":
		qs := []query{{expr: agg(fmt.Sprintf(`rate(container_cpu_usage_seconds_total{%s}[%s])`, containerSel, w)), legend: by}}
		return append(qs, refQueries(sc, metric, sel, nodeLabel)...), "cores", nil
	case "memory":
		qs := []query{{expr: agg(fmt.Sprintf(`container_memory_working_set_bytes{%s}`, containerSel)), legend: by}}
		return append(qs, refQueries(sc, metric, sel, nodeLabel)...), "bytes", nil
	case "network":
		// Network counters are reported on the pod sandbox, so no container filter here.
		return []query{
			{expr: fmt.Sprintf(`sum(rate(container_network_receive_bytes_total{%s}[%s]))`, sel, w), legend: "=rx"},
			{expr: fmt.Sprintf(`sum(rate(container_network_transmit_bytes_total{%s}[%s]))`, sel, w), legend: "=tx"},
		}, "bytes/s", nil
	case "fs":
		return []query{
			{expr: fmt.Sprintf(`sum(rate(container_fs_reads_bytes_total{%s}[%s]))`, containerSel, w), legend: "=read"},
			{expr: fmt.Sprintf(`sum(rate(container_fs_writes_bytes_total{%s}[%s]))`, containerSel, w), legend: "=write"},
		}, "bytes/s", nil
	}
	return nil, "", fmt.Errorf("unknown metric %q", metric)
}

// nodeLabels are tried in order: kube-prometheus-stack relabels cAdvisor with "node"; plain kubelet
// scrapes only have "instance"; older configs use the kubernetes.io/hostname label.
var nodeLabels = []string{"node", "instance", "kubernetes_io_hostname"}

// Chart runs the preset queries for a metric over the last rng.
func (c *Client) Chart(ctx context.Context, cacheKey string, sc Scope, metric string, rng time.Duration) (*Chart, error) {
	step, window := Steps(rng)
	end := time.Now().Truncate(step)
	start := end.Add(-rng)
	labels := []string{""}
	if sc.Kind == "node" || sc.Kind == "cluster" {
		labels = nodeLabels
	}
	var last *Chart
	for _, nl := range labels {
		qs, unit, err := build(sc, metric, window, nl)
		if err != nil {
			return nil, err
		}
		out := &Chart{Metric: metric, Unit: unit, Step: int(step.Seconds()), Series: []Series{}}
		usage := 0
		for _, qq := range qs {
			key := cacheKey + "|" + qq.expr + "|" + strconv.FormatInt(start.Unix(), 10) + "|" + strconv.FormatInt(end.Unix(), 10)
			series, ok := results.get(key)
			if !ok {
				series, err = c.QueryRange(ctx, qq.expr, start, end, step, qq.legend)
				if err != nil {
					if qq.ref != "" {
						continue // reference lines are best effort
					}
					return nil, err
				}
				results.put(key, series)
			}
			out.Queries = append(out.Queries, qq.expr)
			for _, s := range series {
				s.Ref = qq.ref
				if qq.ref != "" && len(s.Points) == 0 {
					continue
				}
				out.Series = append(out.Series, s)
				if qq.ref == "" {
					usage++
				}
			}
		}
		if usage > 0 {
			return out, nil
		}
		last = out
	}
	return last, nil
}
