package project

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

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
	r.Cells["state"] = "Active"
	r.Cells["active"] = int64(len(slice(u, "status", "active")))
	r.Cells["lastSchedule"] = str(u, "status", "lastScheduleTime")
	r.Health = "ok"
	if susp {
		r.Cells["state"], r.Health = "Suspended", "warn"
	}
}

func hpa(u *unstructured.Unstructured, r *Row) {
	r.Cells["target"] = str(u, "spec", "scaleTargetRef", "kind") + "/" + str(u, "spec", "scaleTargetRef", "name")
	minR := num(u, "spec", "minReplicas")
	if _, found, _ := unstructured.NestedFieldNoCopy(u.Object, "spec", "minReplicas"); !found {
		minR = 1
	}
	r.Cells["min"] = minR
	r.Cells["max"] = num(u, "spec", "maxReplicas")
	r.Cells["replicas"] = num(u, "status", "currentReplicas")
	r.Cells["metrics"] = hpaMetrics(u)
	r.Health = "ok"
}

// hpaMetrics summarizes resource metrics as "cpu: 35%/80%" (current/target); other metric types by name.
func hpaMetrics(u *unstructured.Unstructured) string {
	current := map[string]string{}
	for _, m := range slice(u, "status", "currentMetrics") {
		mm, _ := m.(map[string]any)
		if res := mapAt(mm, "resource"); res != nil {
			current[strOr(res["name"], "")] = metricValue(mapAt(res, "current"))
		}
	}
	var parts []string
	for _, m := range slice(u, "spec", "metrics") {
		mm, _ := m.(map[string]any)
		t := strOr(mm["type"], "")
		res := mapAt(mm, "resource")
		if t != "Resource" || res == nil {
			parts = append(parts, t)
			continue
		}
		name := strOr(res["name"], "")
		cur := current[name]
		if cur == "" {
			cur = "?"
		}
		parts = append(parts, fmt.Sprintf("%s: %s/%s", name, cur, metricValue(mapAt(res, "target"))))
	}
	return strings.Join(parts, ", ")
}

func metricValue(m map[string]any) string {
	if m == nil {
		return ""
	}
	if v, ok := m["averageUtilization"]; ok {
		return fmt.Sprintf("%d%%", toInt(v))
	}
	for _, k := range []string{"averageValue", "value"} {
		if v, ok := m[k]; ok {
			return fmt.Sprint(v)
		}
	}
	return ""
}

func pdb(u *unstructured.Unstructured, r *Row) {
	r.Cells["minAvailable"] = str(u, "spec", "minAvailable")
	r.Cells["maxUnavailable"] = str(u, "spec", "maxUnavailable")
	allowed := num(u, "status", "disruptionsAllowed")
	r.Cells["allowed"] = allowed
	r.Health = "ok"
	if allowed == 0 {
		r.Health = "warn"
	}
}
