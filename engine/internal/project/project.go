// Package project turns Kubernetes objects into compact grid rows.
package project

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Column describes a grid column.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"` // text | number | age | status | containers
	Width int    `json:"width,omitempty"`
}

// Row is the compact projection sent to the UI.
type Row struct {
	UID       string            `json:"uid"`
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	Created   string            `json:"created,omitempty"`
	Health    string            `json:"health"` // ok | warn | error | neutral
	Labels    map[string]string `json:"labels,omitempty"`
	Cells     map[string]any    `json:"cells"`
}

type projector struct {
	columns []Column
	fn      func(u *unstructured.Unstructured, r *Row)
}

var base = []Column{{Key: "name", Label: "Name", Width: 280}, {Key: "namespace", Label: "Namespace", Width: 160}}
var ageCol = Column{Key: "age", Label: "Age", Type: "age", Width: 80}

var projectors = map[string]projector{
	"Pod": {cols(Column{Key: "containers", Label: "Containers", Type: "containers", Width: 96}, Column{Key: "ready", Label: "Ready", Width: 70}, Column{Key: "status", Label: "Status", Type: "status", Width: 150},
		Column{Key: "restarts", Label: "Restarts", Type: "number", Width: 80}, Column{Key: "cpu", Label: "CPU", Type: "number", Width: 80},
		Column{Key: "memory", Label: "Memory", Type: "number", Width: 90}, Column{Key: "node", Label: "Node", Width: 180},
		Column{Key: "ip", Label: "IP", Width: 120}), pod},
	"Deployment":              {cols(Column{Key: "ready", Label: "Ready", Width: 80}, Column{Key: "upToDate", Label: "Up-to-date", Type: "number", Width: 90}, Column{Key: "available", Label: "Available", Type: "number", Width: 90}, Column{Key: "status", Label: "Status", Type: "status", Width: 110}), workload},
	"StatefulSet":             {cols(Column{Key: "ready", Label: "Ready", Width: 80}, Column{Key: "status", Label: "Status", Type: "status", Width: 110}), workload},
	"ReplicaSet":              {cols(Column{Key: "desired", Label: "Desired", Type: "number", Width: 80}, Column{Key: "ready", Label: "Ready", Width: 80}, Column{Key: "status", Label: "Status", Type: "status", Width: 110}), workload},
	"DaemonSet":               {cols(Column{Key: "desired", Label: "Desired", Type: "number", Width: 80}, Column{Key: "ready", Label: "Ready", Width: 80}, Column{Key: "status", Label: "Status", Type: "status", Width: 110}), daemonSet},
	"Job":                     {cols(Column{Key: "completions", Label: "Completions", Width: 100}, Column{Key: "status", Label: "Status", Type: "status", Width: 110}), job},
	"CronJob":                 {cols(Column{Key: "schedule", Label: "Schedule", Width: 130}, Column{Key: "suspend", Label: "Suspend", Width: 80}, Column{Key: "active", Label: "Active", Type: "number", Width: 70}, Column{Key: "lastSchedule", Label: "Last schedule", Type: "age", Width: 110}), cronJob},
	"Service":                 {cols(Column{Key: "type", Label: "Type", Width: 110}, Column{Key: "clusterIP", Label: "Cluster IP", Width: 120}, Column{Key: "ports", Label: "Ports", Width: 200}, Column{Key: "selector", Label: "Selector", Width: 220}), service},
	"Ingress":                 {cols(Column{Key: "class", Label: "Class", Width: 100}, Column{Key: "hosts", Label: "Hosts", Width: 260}, Column{Key: "address", Label: "Address", Width: 140}), ingress},
	"ConfigMap":               {cols(Column{Key: "keys", Label: "Keys", Type: "number", Width: 70}), configMap},
	"Secret":                  {cols(Column{Key: "type", Label: "Type", Width: 260}, Column{Key: "keys", Label: "Keys", Type: "number", Width: 70}), secret},
	"Node":                    {[]Column{{Key: "name", Label: "Name", Width: 260}, {Key: "status", Label: "Status", Type: "status", Width: 110}, {Key: "roles", Label: "Roles", Width: 140}, {Key: "version", Label: "Version", Width: 110}, {Key: "cpu", Label: "CPU", Type: "number", Width: 80}, {Key: "memory", Label: "Memory", Type: "number", Width: 90}, {Key: "internalIP", Label: "Internal IP", Width: 130}, ageCol}, node},
	"Namespace":               {[]Column{{Key: "name", Label: "Name", Width: 300}, {Key: "status", Label: "Status", Type: "status", Width: 110}, ageCol}, namespace},
	"Event":                   {[]Column{{Key: "type", Label: "Type", Type: "status", Width: 100}, {Key: "reason", Label: "Reason", Width: 150}, {Key: "object", Label: "Object", Width: 240}, {Key: "message", Label: "Message", Width: 420}, {Key: "count", Label: "Count", Type: "number", Width: 70}, {Key: "namespace", Label: "Namespace", Width: 140}, {Key: "lastSeen", Label: "Last seen", Type: "age", Width: 90}}, event},
	"PersistentVolumeClaim":   {cols(Column{Key: "status", Label: "Status", Type: "status", Width: 100}, Column{Key: "volume", Label: "Volume", Width: 220}, Column{Key: "capacity", Label: "Capacity", Width: 90}, Column{Key: "storageClass", Label: "Storage class", Width: 140}), pvc},
	"PersistentVolume":        {[]Column{{Key: "name", Label: "Name", Width: 260}, {Key: "capacity", Label: "Capacity", Width: 90}, {Key: "status", Label: "Status", Type: "status", Width: 100}, {Key: "claim", Label: "Claim", Width: 240}, {Key: "storageClass", Label: "Storage class", Width: 140}, ageCol}, pv},
	"HorizontalPodAutoscaler": {cols(Column{Key: "target", Label: "Target", Width: 200}, Column{Key: "replicas", Label: "Replicas", Width: 120}), hpa},
}

func cols(extra ...Column) []Column {
	out := append([]Column{}, base...)
	out = append(out, extra...)
	return append(out, ageCol)
}

// Columns returns the columns for a kind; namespaced controls the generic fallback.
func Columns(kind string, namespaced bool) []Column {
	if p, ok := projectors[kind]; ok {
		return p.columns
	}
	if namespaced {
		return []Column{base[0], base[1], ageCol}
	}
	return []Column{base[0], ageCol}
}

// Project builds a row for any object.
func Project(u *unstructured.Unstructured) Row {
	r := Row{UID: string(u.GetUID()), Name: u.GetName(), Namespace: u.GetNamespace(), Health: "neutral", Labels: u.GetLabels(), Cells: map[string]any{}}
	if ts := u.GetCreationTimestamp(); !ts.IsZero() {
		r.Created = ts.UTC().Format(time.RFC3339)
	}
	if r.UID == "" {
		r.UID = r.Namespace + "/" + r.Name
	}
	if p, ok := projectors[u.GetKind()]; ok {
		p.fn(u, &r)
	} else {
		genericConditions(u, &r)
	}
	if u.GetDeletionTimestamp() != nil {
		r.Cells["status"] = "Terminating"
		r.Health = "warn"
	}
	return r
}

func str(u *unstructured.Unstructured, path ...string) string {
	v, _, _ := unstructured.NestedFieldNoCopy(u.Object, path...)
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

func num(u *unstructured.Unstructured, path ...string) int64 {
	v, _, _ := unstructured.NestedFieldNoCopy(u.Object, path...)
	switch t := v.(type) {
	case int64:
		return t
	case float64:
		return int64(t)
	case int:
		return int64(t)
	}
	return 0
}

func slice(u *unstructured.Unstructured, path ...string) []any {
	v, _, _ := unstructured.NestedFieldNoCopy(u.Object, path...)
	s, _ := v.([]any)
	return s
}

func mapAt(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

// ContainerState is one square of the Pod "Containers" column.
type ContainerState struct {
	Name  string `json:"name"`
	State string `json:"state"` // running | waiting | error | done | init
}

// containerStates lists the app containers in spec order (init containers too while the pod initializes).
func containerStates(u *unstructured.Unstructured, initializing bool) []ContainerState {
	byName := map[string]map[string]any{}
	for _, s := range append(slice(u, "status", "containerStatuses"), slice(u, "status", "initContainerStatuses")...) {
		if cs, ok := s.(map[string]any); ok {
			byName[strOr(cs["name"], "")] = cs
		}
	}
	out := []ContainerState{}
	if initializing {
		for _, c := range slice(u, "spec", "initContainers") {
			cm, _ := c.(map[string]any)
			name := strOr(cm["name"], "")
			if st := stateOf(byName[name]); st != "done" {
				if st != "error" {
					st = "init"
				}
				out = append(out, ContainerState{name, st})
			}
		}
	}
	for _, c := range slice(u, "spec", "containers") {
		cm, _ := c.(map[string]any)
		name := strOr(cm["name"], "")
		out = append(out, ContainerState{name, stateOf(byName[name])})
	}
	return out
}

func stateOf(cs map[string]any) string {
	if cs == nil {
		return "waiting"
	}
	st := mapAt(cs, "state")
	switch {
	case mapAt(st, "running") != nil:
		if ready, _ := cs["ready"].(bool); ready {
			return "running"
		}
		return "waiting"
	case mapAt(st, "terminated") != nil:
		if toInt(mapAt(st, "terminated")["exitCode"]) == 0 {
			return "done"
		}
		return "error"
	case mapAt(st, "waiting") != nil:
		reason := strOr(mapAt(st, "waiting")["reason"], "")
		if strings.Contains(reason, "BackOff") || strings.Contains(reason, "Err") || strings.Contains(reason, "Invalid") {
			return "error"
		}
	}
	return "waiting"
}

func pod(u *unstructured.Unstructured, r *Row) {
	statuses := slice(u, "status", "containerStatuses")
	specContainers := slice(u, "spec", "containers")
	ready, restarts := 0, int64(0)
	reason := str(u, "status", "phase")
	if pr := str(u, "status", "reason"); pr != "" {
		reason = pr
	}
	initializing := false
	for i, s := range slice(u, "status", "initContainerStatuses") {
		cs, _ := s.(map[string]any)
		restarts += toInt(cs["restartCount"])
		st := mapAt(cs, "state")
		if t := mapAt(st, "terminated"); t != nil && toInt(t["exitCode"]) == 0 {
			continue
		}
		initializing = true
		if t := mapAt(st, "terminated"); t != nil {
			reason = "Init:" + strOr(t["reason"], fmt.Sprintf("ExitCode:%d", toInt(t["exitCode"])))
		} else if w := mapAt(st, "waiting"); w != nil && strOr(w["reason"], "") != "" && w["reason"] != "PodInitializing" {
			reason = "Init:" + strOr(w["reason"], "")
		} else {
			reason = fmt.Sprintf("Init:%d/%d", i, len(slice(u, "spec", "initContainers")))
		}
		break
	}
	if !initializing {
		hasRunning := false
		for _, s := range statuses {
			cs, _ := s.(map[string]any)
			restarts += toInt(cs["restartCount"])
			if b, _ := cs["ready"].(bool); b {
				ready++
			}
			st := mapAt(cs, "state")
			if w := mapAt(st, "waiting"); w != nil && strOr(w["reason"], "") != "" {
				reason = strOr(w["reason"], "")
			} else if t := mapAt(st, "terminated"); t != nil {
				reason = strOr(t["reason"], fmt.Sprintf("ExitCode:%d", toInt(t["exitCode"])))
			} else if mapAt(st, "running") != nil {
				hasRunning = true
			}
		}
		if reason == "Completed" && hasRunning {
			reason = "Running"
		}
	}
	r.Cells["ready"] = fmt.Sprintf("%d/%d", ready, len(specContainers))
	r.Cells["status"] = reason
	r.Cells["restarts"] = restarts
	r.Cells["node"] = str(u, "spec", "nodeName")
	r.Cells["ip"] = str(u, "status", "podIP")
	r.Cells["containers"] = containerStates(u, initializing)
	switch {
	case reason == "Running" && ready == len(specContainers):
		r.Health = "ok"
	case reason == "Succeeded" || reason == "Completed":
		r.Health = "neutral"
	case reason == "Pending" || reason == "ContainerCreating" || reason == "Running" || strings.HasPrefix(reason, "Init:") && !strings.Contains(reason, "Err") && !strings.Contains(reason, "BackOff"):
		r.Health = "warn"
	default:
		r.Health = "error"
	}
}

func workload(u *unstructured.Unstructured, r *Row) {
	desired := num(u, "spec", "replicas")
	if _, found, _ := unstructured.NestedFieldNoCopy(u.Object, "spec", "replicas"); !found {
		desired = 1
	}
	ready := num(u, "status", "readyReplicas")
	updated := num(u, "status", "updatedReplicas")
	available := num(u, "status", "availableReplicas")
	r.Cells["ready"] = fmt.Sprintf("%d/%d", ready, desired)
	r.Cells["desired"] = desired
	r.Cells["upToDate"] = updated
	r.Cells["available"] = available
	status, health := workloadStatus(u, desired, ready, updated, available)
	r.Cells["status"] = status
	r.Health = health
}

// workloadStatus classifies a replica-based workload into Healthy/Scaling/Updating/Pending/Blocked/Degraded.
func workloadStatus(u *unstructured.Unstructured, desired, ready, updated, available int64) (string, string) {
	for _, c := range slice(u, "status", "conditions") {
		cm, _ := c.(map[string]any)
		t, s, reason := strOr(cm["type"], ""), strOr(cm["status"], ""), strOr(cm["reason"], "")
		if t == "Progressing" && s == "False" && reason == "ProgressDeadlineExceeded" {
			return "Blocked", "error"
		}
		if t == "ReplicaFailure" && s == "True" {
			return "Degraded", "error"
		}
	}
	switch {
	case desired == 0:
		return "Scaled to 0", "neutral"
	case ready == desired && (updated == 0 || updated == desired) && available >= desired:
		return "Healthy", "ok"
	case updated < desired && updated > 0:
		return "Updating", "warn"
	case ready < desired && ready > 0:
		return "Scaling", "warn"
	default:
		return "Pending", "warn"
	}
}

func daemonSet(u *unstructured.Unstructured, r *Row) {
	desired := num(u, "status", "desiredNumberScheduled")
	ready := num(u, "status", "numberReady")
	r.Cells["desired"] = desired
	r.Cells["ready"] = fmt.Sprintf("%d/%d", ready, desired)
	if desired == ready {
		r.Cells["status"], r.Health = "Healthy", "ok"
	} else {
		r.Cells["status"], r.Health = "Updating", "warn"
	}
}

func job(u *unstructured.Unstructured, r *Row) {
	comp := num(u, "spec", "completions")
	if comp == 0 {
		comp = 1
	}
	succ := num(u, "status", "succeeded")
	r.Cells["completions"] = fmt.Sprintf("%d/%d", succ, comp)
	r.Cells["status"], r.Health = "Running", "warn"
	for _, c := range slice(u, "status", "conditions") {
		cm, _ := c.(map[string]any)
		if cm["status"] != "True" {
			continue
		}
		switch cm["type"] {
		case "Complete":
			r.Cells["status"], r.Health = "Complete", "ok"
		case "Failed":
			r.Cells["status"], r.Health = "Failed", "error"
		}
	}
}

func cronJob(u *unstructured.Unstructured, r *Row) {
	r.Cells["schedule"] = str(u, "spec", "schedule")
	susp, _, _ := unstructured.NestedBool(u.Object, "spec", "suspend")
	r.Cells["suspend"] = fmt.Sprint(susp)
	r.Cells["active"] = int64(len(slice(u, "status", "active")))
	r.Cells["lastSchedule"] = str(u, "status", "lastScheduleTime")
	r.Health = "ok"
	if susp {
		r.Health = "neutral"
	}
}

func service(u *unstructured.Unstructured, r *Row) {
	r.Cells["type"] = str(u, "spec", "type")
	r.Cells["clusterIP"] = str(u, "spec", "clusterIP")
	var ports []string
	for _, p := range slice(u, "spec", "ports") {
		pm, _ := p.(map[string]any)
		s := fmt.Sprintf("%v/%v", pm["port"], strOr(pm["protocol"], "TCP"))
		if np, ok := pm["nodePort"]; ok {
			s = fmt.Sprintf("%v:%v/%v", pm["port"], np, strOr(pm["protocol"], "TCP"))
		}
		ports = append(ports, s)
	}
	r.Cells["ports"] = strings.Join(ports, ", ")
	sel, _, _ := unstructured.NestedStringMap(u.Object, "spec", "selector")
	r.Cells["selector"] = joinMap(sel)
	r.Health = "ok"
}

func ingress(u *unstructured.Unstructured, r *Row) {
	r.Cells["class"] = str(u, "spec", "ingressClassName")
	var hosts []string
	for _, rule := range slice(u, "spec", "rules") {
		rm, _ := rule.(map[string]any)
		hosts = append(hosts, strOr(rm["host"], "*"))
	}
	r.Cells["hosts"] = strings.Join(hosts, ", ")
	var addrs []string
	for _, a := range slice(u, "status", "loadBalancer", "ingress") {
		am, _ := a.(map[string]any)
		addrs = append(addrs, strOr(am["ip"], strOr(am["hostname"], "")))
	}
	r.Cells["address"] = strings.Join(addrs, ", ")
	r.Health = "ok"
}

func configMap(u *unstructured.Unstructured, r *Row) {
	d, _, _ := unstructured.NestedMap(u.Object, "data")
	b, _, _ := unstructured.NestedMap(u.Object, "binaryData")
	r.Cells["keys"] = int64(len(d) + len(b))
}

func secret(u *unstructured.Unstructured, r *Row) {
	r.Cells["type"] = str(u, "type")
	d, _, _ := unstructured.NestedMap(u.Object, "data")
	r.Cells["keys"] = int64(len(d))
}

func node(u *unstructured.Unstructured, r *Row) {
	status, health := "Unknown", "error"
	for _, c := range slice(u, "status", "conditions") {
		cm, _ := c.(map[string]any)
		if cm["type"] == "Ready" {
			if cm["status"] == "True" {
				status, health = "Ready", "ok"
			} else {
				status, health = "NotReady", "error"
			}
		}
	}
	if b, _, _ := unstructured.NestedBool(u.Object, "spec", "unschedulable"); b {
		status += ",SchedulingDisabled"
		if health == "ok" {
			health = "warn"
		}
	}
	var roles []string
	for k := range u.GetLabels() {
		if strings.HasPrefix(k, "node-role.kubernetes.io/") {
			roles = append(roles, strings.TrimPrefix(k, "node-role.kubernetes.io/"))
		}
	}
	sort.Strings(roles)
	r.Cells["status"] = status
	r.Cells["roles"] = strings.Join(roles, ",")
	r.Cells["version"] = str(u, "status", "nodeInfo", "kubeletVersion")
	for _, a := range slice(u, "status", "addresses") {
		am, _ := a.(map[string]any)
		if am["type"] == "InternalIP" {
			r.Cells["internalIP"] = am["address"]
		}
	}
	r.Health = health
}

func namespace(u *unstructured.Unstructured, r *Row) {
	p := str(u, "status", "phase")
	r.Cells["status"] = p
	r.Health = map[bool]string{true: "ok", false: "warn"}[p == "Active"]
}

func event(u *unstructured.Unstructured, r *Row) {
	t := str(u, "type")
	r.Cells["type"] = t
	r.Cells["reason"] = str(u, "reason")
	kind, name := str(u, "involvedObject", "kind"), str(u, "involvedObject", "name")
	if kind == "" {
		kind, name = str(u, "regarding", "kind"), str(u, "regarding", "name")
	}
	r.Cells["object"] = kind + "/" + name
	msg := str(u, "message")
	if msg == "" {
		msg = str(u, "note")
	}
	r.Cells["message"] = msg
	cnt := num(u, "count")
	if cnt == 0 {
		cnt = num(u, "series", "count")
	}
	if cnt == 0 {
		cnt = 1
	}
	r.Cells["count"] = cnt
	last := str(u, "lastTimestamp")
	if last == "" {
		last = str(u, "eventTime")
	}
	if last == "" {
		last = r.Created
	}
	r.Cells["lastSeen"] = last
	r.Health = map[bool]string{true: "warn", false: "neutral"}[t == "Warning"]
}

func pvc(u *unstructured.Unstructured, r *Row) {
	p := str(u, "status", "phase")
	r.Cells["status"] = p
	r.Cells["volume"] = str(u, "spec", "volumeName")
	r.Cells["capacity"] = str(u, "status", "capacity", "storage")
	r.Cells["storageClass"] = str(u, "spec", "storageClassName")
	r.Health = map[string]string{"Bound": "ok", "Pending": "warn", "Lost": "error"}[p]
	if r.Health == "" {
		r.Health = "neutral"
	}
}

func pv(u *unstructured.Unstructured, r *Row) {
	p := str(u, "status", "phase")
	r.Cells["status"] = p
	r.Cells["capacity"] = str(u, "spec", "capacity", "storage")
	if ns, n := str(u, "spec", "claimRef", "namespace"), str(u, "spec", "claimRef", "name"); n != "" {
		r.Cells["claim"] = ns + "/" + n
	}
	r.Cells["storageClass"] = str(u, "spec", "storageClassName")
	r.Health = map[string]string{"Bound": "ok", "Available": "neutral", "Released": "warn", "Failed": "error"}[p]
	if r.Health == "" {
		r.Health = "neutral"
	}
}

func hpa(u *unstructured.Unstructured, r *Row) {
	r.Cells["target"] = str(u, "spec", "scaleTargetRef", "kind") + "/" + str(u, "spec", "scaleTargetRef", "name")
	r.Cells["replicas"] = fmt.Sprintf("%d (%d-%d)", num(u, "status", "currentReplicas"), num(u, "spec", "minReplicas"), num(u, "spec", "maxReplicas"))
	r.Health = "ok"
}

func genericConditions(u *unstructured.Unstructured, r *Row) {
	for _, c := range slice(u, "status", "conditions") {
		cm, _ := c.(map[string]any)
		if cm["type"] == "Ready" || cm["type"] == "Available" {
			if cm["status"] == "True" {
				r.Health = "ok"
			} else {
				r.Health = "warn"
			}
		}
	}
}

func toInt(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case float64:
		return int64(t)
	case int:
		return int64(t)
	}
	return 0
}

func strOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

func joinMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}
