package project

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

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

func ingressClass(u *unstructured.Unstructured, r *Row) {
	r.Cells["controller"] = str(u, "spec", "controller")
}

func endpointSlice(u *unstructured.Unstructured, r *Row) {
	r.Cells["addressType"] = str(u, "addressType")
	var ports []string
	for _, p := range slice(u, "ports") {
		pm, _ := p.(map[string]any)
		ports = append(ports, fmt.Sprintf("%v/%v", pm["port"], strOr(pm["protocol"], "TCP")))
	}
	r.Cells["ports"] = strings.Join(ports, ", ")
	r.Cells["endpoints"] = int64(len(slice(u, "endpoints")))
}

func endpoints(u *unstructured.Unstructured, r *Row) {
	var addrs []string
	for _, s := range slice(u, "subsets") {
		sm, _ := s.(map[string]any)
		var ports []string
		for _, p := range asSlice(sm["ports"]) {
			pm, _ := p.(map[string]any)
			ports = append(ports, fmt.Sprint(pm["port"]))
		}
		for _, a := range asSlice(sm["addresses"]) {
			am, _ := a.(map[string]any)
			ip := strOr(am["ip"], "")
			if len(ports) == 0 {
				addrs = append(addrs, ip)
			}
			for _, p := range ports {
				addrs = append(addrs, ip+":"+p)
			}
		}
	}
	r.Cells["addresses"] = strings.Join(addrs, ", ")
}

func networkPolicy(u *unstructured.Unstructured, r *Row) {
	sel, _, _ := unstructured.NestedStringMap(u.Object, "spec", "podSelector", "matchLabels")
	if s := joinMap(sel); s != "" {
		r.Cells["podSelector"] = s
	} else if len(slice(u, "spec", "podSelector", "matchExpressions")) == 0 {
		r.Cells["podSelector"] = "(todos os pods)"
	}
	var types []string
	for _, t := range slice(u, "spec", "policyTypes") {
		types = append(types, fmt.Sprint(t))
	}
	r.Cells["policyTypes"] = strings.Join(types, ", ")
}
