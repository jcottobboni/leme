package project

import (
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

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

func crd(u *unstructured.Unstructured, r *Row) {
	r.Cells["group"] = str(u, "spec", "group")
	var versions []string
	for _, v := range slice(u, "spec", "versions") {
		vm, _ := v.(map[string]any)
		if served, _ := vm["served"].(bool); served {
			versions = append(versions, strOr(vm["name"], ""))
		}
	}
	r.Cells["versions"] = strings.Join(versions, ", ")
	r.Cells["scope"] = str(u, "spec", "scope")
}

func binding(u *unstructured.Unstructured, r *Row) {
	r.Cells["role"] = str(u, "roleRef", "kind") + "/" + str(u, "roleRef", "name")
	subjects := slice(u, "subjects")
	if len(subjects) == 0 {
		return
	}
	sm, _ := subjects[0].(map[string]any)
	s := strOr(sm["kind"], "") + "/" + strOr(sm["name"], "")
	if len(subjects) > 1 {
		s += fmt.Sprintf(" +%d", len(subjects)-1)
	}
	r.Cells["subjects"] = s
}

func rbacRole(u *unstructured.Unstructured, r *Row) {
	r.Cells["rules"] = int64(len(slice(u, "rules")))
}

func priorityClass(u *unstructured.Unstructured, r *Row) {
	r.Cells["value"] = num(u, "value")
	if b, _, _ := unstructured.NestedBool(u.Object, "globalDefault"); b {
		r.Cells["globalDefault"] = "Sim"
	}
}

func runtimeClass(u *unstructured.Unstructured, r *Row) {
	r.Cells["handler"] = str(u, "handler")
}

func lease(u *unstructured.Unstructured, r *Row) {
	r.Cells["holder"] = str(u, "spec", "holderIdentity")
}
