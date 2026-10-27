package project

import (
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// GatewayGroup is the API group of the Gateway API (gateway.networking.k8s.io). Its kinds are
// projected by group and kind: Istio's networking.istio.io also has a Gateway, with other fields.
const GatewayGroup = "gateway.networking.k8s.io"

// Gateway API states shown in the "Status" cell.
const (
	GwProgrammed    = "Programado"
	GwNotProgrammed = "Não programado"
	GwNotAccepted   = "Não aceito"
	GwPending       = "Pendente"
	GwAccepted      = "Aceito"
	RouteAccepted   = "Aceita"
	RouteNotAccept  = "Não aceita"
	RouteUnresolved = "Refs não resolvidas"
	RouteNoStatus   = "Sem status"
)

// groupProjectors are keyed by "group/kind": they win over the kind-only table.
var groupProjectors = map[string]projector{
	GatewayGroup + "/GatewayClass": {clusterCols(Column{Key: "controller", Label: "Controlador", Width: 260}, Column{Key: "status", Label: "Status", Type: "status", Width: 120}), gatewayClass},
	GatewayGroup + "/Gateway": {cols(Column{Key: "class", Label: "Classe", Width: 130}, Column{Key: "listeners", Label: "Listeners", Width: 240},
		Column{Key: "addresses", Label: "Endereços", Width: 150}, Column{Key: "routes", Label: "Rotas", Type: "number", Width: 70},
		Column{Key: "status", Label: "Status", Type: "status", Width: 130}), gateway},
	GatewayGroup + "/HTTPRoute":      {routeCols(true), gwRoute},
	GatewayGroup + "/GRPCRoute":      {routeCols(true), gwRoute},
	GatewayGroup + "/TLSRoute":       {routeCols(true), gwRoute},
	GatewayGroup + "/TCPRoute":       {routeCols(false), gwRoute},
	GatewayGroup + "/UDPRoute":       {routeCols(false), gwRoute},
	GatewayGroup + "/ReferenceGrant": {cols(Column{Key: "from", Label: "De", Width: 260}, Column{Key: "to", Label: "Para", Width: 220}), referenceGrant},
	GatewayGroup + "/BackendTLSPolicy": {cols(Column{Key: "targets", Label: "Alvos", Width: 220}, Column{Key: "hostname", Label: "Hostname", Width: 200},
		Column{Key: "status", Label: "Status", Type: "status", Width: 120}), backendTLSPolicy},
	GatewayGroup + "/ListenerSet": {cols(Column{Key: "parent", Label: "Gateway", Width: 200}, Column{Key: "listeners", Label: "Listeners", Width: 240},
		Column{Key: "status", Label: "Status", Type: "status", Width: 130}), listenerSet},
}

func routeCols(hostnames bool) []Column {
	var c []Column
	if hostnames {
		c = append(c, Column{Key: "hostnames", Label: "Hostnames", Width: 200})
	}
	c = append(c, Column{Key: "parents", Label: "Parents", Width: 200}, Column{Key: "backends", Label: "Backends", Width: 220},
		Column{Key: "status", Label: "Status", Type: "status", Width: 140})
	return cols(c...)
}

// projectorFor returns the projector of a group and kind: the group-specific table first, then the
// kind table for built-in groups only.
func projectorFor(group, kind string) (projector, bool) {
	if p, ok := groupProjectors[group+"/"+kind]; ok {
		return p, true
	}
	return projector{}, false
}

// conditionIn finds a condition in a list of conditions (status.conditions or a parent's).
func conditionIn(list []any, typ string) (map[string]any, bool) {
	for _, c := range list {
		if cm, ok := c.(map[string]any); ok && cm["type"] == typ {
			return cm, true
		}
	}
	return nil, false
}

func gatewayClass(u *unstructured.Unstructured, r *Row) {
	r.Cells["controller"] = str(u, "spec", "controllerName")
	r.Cells["status"], r.Health = acceptedState(slice(u, "status", "conditions"))
}

// acceptedState reads an Accepted condition (GatewayClass, BackendTLSPolicy ancestors).
func acceptedState(conds []any) (string, string) {
	c, ok := conditionIn(conds, "Accepted")
	switch {
	case !ok:
		return RouteNoStatus, "neutral"
	case c["status"] == "True":
		return GwAccepted, "ok"
	case c["status"] == "False":
		return GwNotAccepted, "error"
	}
	return GwPending, "warn"
}

// GatewayState is the semantic status of a Gateway from its Accepted and Programmed conditions.
func GatewayState(u *unstructured.Unstructured) (state, health string) {
	conds := slice(u, "status", "conditions")
	acc, hasAcc := conditionIn(conds, "Accepted")
	prog, hasProg := conditionIn(conds, "Programmed")
	switch {
	case hasAcc && acc["status"] == "False":
		return GwNotAccepted, "error"
	case hasProg && prog["status"] == "True":
		return GwProgrammed, "ok"
	case hasProg && prog["status"] == "False":
		if prog["reason"] == "Pending" {
			return GwPending, "warn"
		}
		return GwNotProgrammed, "error"
	case !hasAcc && !hasProg:
		return RouteNoStatus, "neutral"
	}
	return GwPending, "warn"
}

func gateway(u *unstructured.Unstructured, r *Row) {
	r.Cells["class"] = str(u, "spec", "gatewayClassName")
	r.Cells["listeners"] = listeners(slice(u, "spec", "listeners"))
	var addrs []string
	for _, a := range slice(u, "status", "addresses") {
		am, _ := a.(map[string]any)
		if v := strOr(am["value"], ""); v != "" {
			addrs = append(addrs, v)
		}
	}
	if len(addrs) == 0 {
		for _, a := range slice(u, "spec", "addresses") {
			am, _ := a.(map[string]any)
			if v := strOr(am["value"], ""); v != "" {
				addrs = append(addrs, v)
			}
		}
	}
	r.Cells["addresses"] = strings.Join(addrs, ", ")
	var attached int64
	for _, l := range slice(u, "status", "listeners") {
		lm, _ := l.(map[string]any)
		attached += toInt(lm["attachedRoutes"])
	}
	r.Cells["routes"] = attached
	r.Cells["status"], r.Health = GatewayState(u)
}

func listeners(list []any) string {
	var out []string
	for _, l := range list {
		lm, _ := l.(map[string]any)
		s := fmt.Sprintf("%s:%v/%s", strOr(lm["name"], "?"), lm["port"], strOr(lm["protocol"], "?"))
		if h := strOr(lm["hostname"], ""); h != "" {
			s += " " + h
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

// ParentRef is a route's parentRefs entry with the namespace defaulted.
type ParentRef struct {
	Group, Kind, Namespace, Name, SectionName string
}

// String renders the parent as "ns/name" (with ":section" when set).
func (p ParentRef) String() string {
	s := p.Namespace + "/" + p.Name
	if p.Kind != "" && p.Kind != "Gateway" {
		s = p.Kind + " " + s
	}
	if p.SectionName != "" {
		s += ":" + p.SectionName
	}
	return s
}

// RouteParents returns the parentRefs of a Gateway API route (default kind Gateway, namespace of the route).
func RouteParents(u *unstructured.Unstructured) []ParentRef {
	var out []ParentRef
	for _, p := range slice(u, "spec", "parentRefs") {
		pm, _ := p.(map[string]any)
		out = append(out, ParentRef{Group: strOr(pm["group"], GatewayGroup), Kind: strOr(pm["kind"], "Gateway"),
			Namespace: strOr(pm["namespace"], u.GetNamespace()), Name: strOr(pm["name"], ""), SectionName: strOr(pm["sectionName"], "")})
	}
	return out
}

// BackendRef is a route's backendRefs entry with group, kind and namespace defaulted.
type BackendRef struct {
	Group, Kind, Namespace, Name string
	Port                         int64
}

// IsService reports whether the backend is a core Service (the default).
func (b BackendRef) IsService() bool { return b.Group == "" && b.Kind == "Service" }

// String renders the backend as "name:port" (ns/ prefix when in another namespace of from).
func (b BackendRef) String(from string) string {
	s := b.Name
	if b.Namespace != from {
		s = b.Namespace + "/" + s
	}
	if !b.IsService() {
		s = b.Kind + " " + s
	}
	if b.Port != 0 {
		s += fmt.Sprintf(":%d", b.Port)
	}
	return s
}

// RouteBackends returns the backendRefs of every rule of a route, deduplicated, in order.
func RouteBackends(u *unstructured.Unstructured) []BackendRef {
	var out []BackendRef
	seen := map[BackendRef]bool{}
	for _, rule := range slice(u, "spec", "rules") {
		rm, _ := rule.(map[string]any)
		for _, b := range asSlice(rm["backendRefs"]) {
			bm, _ := b.(map[string]any)
			br := BackendRef{Group: strOr(bm["group"], ""), Kind: strOr(bm["kind"], "Service"), Namespace: strOr(bm["namespace"], u.GetNamespace()),
				Name: strOr(bm["name"], ""), Port: toInt(bm["port"])}
			if br.Name == "" || seen[br] {
				continue
			}
			seen[br] = true
			out = append(out, br)
		}
	}
	return out
}

// RouteParentStatus is one entry of status.parents of a route.
type RouteParentStatus struct {
	Parent       ParentRef
	Controller   string
	Accepted     map[string]any // nil when absent
	ResolvedRefs map[string]any
}

// RouteStatuses reads status.parents of a route.
func RouteStatuses(u *unstructured.Unstructured) []RouteParentStatus {
	var out []RouteParentStatus
	for _, p := range slice(u, "status", "parents") {
		pm, _ := p.(map[string]any)
		ref := mapAt(pm, "parentRef")
		s := RouteParentStatus{Controller: strOr(pm["controllerName"], ""),
			Parent: ParentRef{Group: strOr(ref["group"], GatewayGroup), Kind: strOr(ref["kind"], "Gateway"), Namespace: strOr(ref["namespace"], u.GetNamespace()),
				Name: strOr(ref["name"], ""), SectionName: strOr(ref["sectionName"], "")}}
		conds := asSlice(pm["conditions"])
		s.Accepted, _ = conditionIn(conds, "Accepted")
		s.ResolvedRefs, _ = conditionIn(conds, "ResolvedRefs")
		out = append(out, s)
	}
	return out
}

// RouteState is the semantic status of a route across its parents: any parent not accepting it is
// an error, then unresolved references; accepted by every parent is ok.
func RouteState(u *unstructured.Unstructured) (state, health string) {
	st := RouteStatuses(u)
	if len(st) == 0 {
		return RouteNoStatus, "neutral"
	}
	accepted := true
	for _, s := range st {
		if s.Accepted != nil && s.Accepted["status"] == "False" {
			return RouteNotAccept, "error"
		}
		if s.Accepted == nil || s.Accepted["status"] != "True" {
			accepted = false
		}
	}
	for _, s := range st {
		if s.ResolvedRefs != nil && s.ResolvedRefs["status"] == "False" {
			return RouteUnresolved, "error"
		}
	}
	if !accepted {
		return GwPending, "warn"
	}
	return RouteAccepted, "ok"
}

func gwRoute(u *unstructured.Unstructured, r *Row) {
	var hosts []string
	for _, h := range slice(u, "spec", "hostnames") {
		hosts = append(hosts, fmt.Sprint(h))
	}
	r.Cells["hostnames"] = strings.Join(hosts, ", ")
	var parents []string
	for _, p := range RouteParents(u) {
		parents = append(parents, p.String())
	}
	r.Cells["parents"] = strings.Join(parents, ", ")
	var backends []string
	for _, b := range RouteBackends(u) {
		backends = append(backends, b.String(u.GetNamespace()))
	}
	r.Cells["backends"] = strings.Join(backends, ", ")
	r.Cells["status"], r.Health = RouteState(u)
}

func referenceGrant(u *unstructured.Unstructured, r *Row) {
	var from, to []string
	for _, f := range slice(u, "spec", "from") {
		fm, _ := f.(map[string]any)
		from = append(from, fmt.Sprintf("%s/%s", strOr(fm["namespace"], "?"), strOr(fm["kind"], "?")))
	}
	for _, t := range slice(u, "spec", "to") {
		tm, _ := t.(map[string]any)
		s := strOr(tm["kind"], "?")
		if n := strOr(tm["name"], ""); n != "" {
			s += " " + n
		}
		to = append(to, s)
	}
	sort.Strings(from)
	r.Cells["from"] = strings.Join(from, ", ")
	r.Cells["to"] = strings.Join(to, ", ")
}

func backendTLSPolicy(u *unstructured.Unstructured, r *Row) {
	var targets []string
	for _, t := range slice(u, "spec", "targetRefs") {
		tm, _ := t.(map[string]any)
		targets = append(targets, strOr(tm["kind"], "Service")+" "+strOr(tm["name"], ""))
	}
	r.Cells["targets"] = strings.Join(targets, ", ")
	r.Cells["hostname"] = str(u, "spec", "validation", "hostname")
	// Status is per ancestor (Gateway): the worst one decides.
	state, health := RouteNoStatus, "neutral"
	for _, a := range slice(u, "status", "ancestors") {
		am, _ := a.(map[string]any)
		s, h := acceptedState(asSlice(am["conditions"]))
		if h == "error" || health == "neutral" || (h == "warn" && health == "ok") {
			state, health = s, h
		}
	}
	r.Cells["status"], r.Health = state, health
}

func listenerSet(u *unstructured.Unstructured, r *Row) {
	r.Cells["parent"] = str(u, "spec", "parentRef", "name")
	r.Cells["listeners"] = listeners(slice(u, "spec", "listeners"))
	r.Cells["status"], r.Health = GatewayState(u)
}
