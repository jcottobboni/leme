// Package netpol evaluates Kubernetes NetworkPolicies: who is isolated, what flows are allowed,
// and whether one pod can reach another. It follows the API semantics, not a specific CNI.
package netpol

import (
	"fmt"
	"net"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// World is the cluster state the evaluator needs.
type World struct {
	Pods       []corev1.Pod
	Namespaces map[string]map[string]string // namespace -> labels
	Policies   []networkingv1.NetworkPolicy
	index      map[string]*corev1.Pod
}

func (w *World) nsLabels(ns string) labels.Set {
	l := w.Namespaces[ns]
	out := labels.Set{}
	for k, v := range l {
		out[k] = v
	}
	// Set by the API server on every namespace since 1.21; selectors rely on it.
	out["kubernetes.io/metadata.name"] = ns
	return out
}

func selects(sel *metav1.LabelSelector, l map[string]string) bool {
	if sel == nil {
		return true
	}
	s, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return false
	}
	return s.Matches(labels.Set(l))
}

func hasType(p *networkingv1.NetworkPolicy, t networkingv1.PolicyType) bool {
	if len(p.Spec.PolicyTypes) == 0 {
		// Default: Ingress always; Egress only when egress rules are present.
		return t == networkingv1.PolicyTypeIngress || (t == networkingv1.PolicyTypeEgress && len(p.Spec.Egress) > 0)
	}
	for _, x := range p.Spec.PolicyTypes {
		if x == t {
			return true
		}
	}
	return false
}

// Selecting returns the policies that select pod for direction t.
func (w *World) Selecting(pod *corev1.Pod, t networkingv1.PolicyType) []*networkingv1.NetworkPolicy {
	var out []*networkingv1.NetworkPolicy
	for i := range w.Policies {
		p := &w.Policies[i]
		if p.Namespace == pod.Namespace && hasType(p, t) && selects(&p.Spec.PodSelector, pod.Labels) {
			out = append(out, p)
		}
	}
	return out
}

// Endpoint is a traffic peer: a pod, or an external IP when Pod is nil.
type Endpoint struct {
	Pod *corev1.Pod
	IP  string
}

func (e Endpoint) ip() string {
	if e.Pod != nil {
		return e.Pod.Status.PodIP
	}
	return e.IP
}

// peerMatches reports whether a rule peer (from/to) matches the remote endpoint, for a policy in ns.
func (w *World) peerMatches(peer networkingv1.NetworkPolicyPeer, policyNS string, remote Endpoint) bool {
	if peer.IPBlock != nil {
		ip := net.ParseIP(remote.ip())
		_, cidr, err := net.ParseCIDR(peer.IPBlock.CIDR)
		if ip == nil || err != nil || !cidr.Contains(ip) {
			return false
		}
		for _, ex := range peer.IPBlock.Except {
			if _, e, err := net.ParseCIDR(ex); err == nil && e.Contains(ip) {
				return false
			}
		}
		return true
	}
	if remote.Pod == nil {
		return false
	}
	if peer.NamespaceSelector != nil {
		if !selects(peer.NamespaceSelector, w.nsLabels(remote.Pod.Namespace)) {
			return false
		}
	} else if remote.Pod.Namespace != policyNS {
		return false
	}
	return peer.PodSelector == nil || selects(peer.PodSelector, remote.Pod.Labels)
}

// portMatches checks a rule port against a destination port. Named ports resolve on the destination pod.
func portMatches(ports []networkingv1.NetworkPolicyPort, port int32, proto corev1.Protocol, dst *corev1.Pod) bool {
	if len(ports) == 0 {
		return true
	}
	for _, p := range ports {
		pp := corev1.ProtocolTCP
		if p.Protocol != nil {
			pp = *p.Protocol
		}
		if pp != proto {
			continue
		}
		if p.Port == nil {
			return true
		}
		if p.Port.Type == intstr.Int {
			lo, hi := p.Port.IntVal, p.Port.IntVal
			if p.EndPort != nil {
				hi = *p.EndPort
			}
			if port >= lo && port <= hi {
				return true
			}
			continue
		}
		if dst != nil && namedPort(dst, p.Port.StrVal, proto) == port {
			return true
		}
	}
	return false
}

func namedPort(pod *corev1.Pod, name string, proto corev1.Protocol) int32 {
	for _, c := range pod.Spec.Containers {
		for _, cp := range c.Ports {
			pr := cp.Protocol
			if pr == "" {
				pr = corev1.ProtocolTCP
			}
			if cp.Name == name && pr == proto {
				return cp.ContainerPort
			}
		}
	}
	return -1
}

// Verdict explains one direction of a flow.
type Verdict struct {
	Isolated  bool     `json:"isolated"`
	Allowed   bool     `json:"allowed"`
	Policies  []string `json:"policies"`            // policies that select the pod in this direction
	AllowedBy []string `json:"allowedBy,omitempty"` // policies with a matching rule
}

// Result of Check.
type Result struct {
	Allowed bool    `json:"allowed"`
	Egress  Verdict `json:"egress"`
	Ingress Verdict `json:"ingress"`
	Reason  string  `json:"reason"`
}

func names(ps []*networkingv1.NetworkPolicy) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.Namespace+"/"+p.Name)
	}
	return out
}

// Check answers whether src can open a connection to dst:port. Either side may be an external IP.
func (w *World) Check(src, dst Endpoint, port int32, proto corev1.Protocol) Result {
	if proto == "" {
		proto = corev1.ProtocolTCP
	}
	r := Result{Egress: Verdict{Allowed: true, Policies: []string{}}, Ingress: Verdict{Allowed: true, Policies: []string{}}}
	if src.Pod != nil {
		ps := w.Selecting(src.Pod, networkingv1.PolicyTypeEgress)
		r.Egress.Policies = names(ps)
		if len(ps) > 0 {
			r.Egress.Isolated, r.Egress.Allowed = true, false
			for _, p := range ps {
				for _, rule := range p.Spec.Egress {
					peerOK := len(rule.To) == 0
					for _, peer := range rule.To {
						if w.peerMatches(peer, p.Namespace, dst) {
							peerOK = true
							break
						}
					}
					if peerOK && portMatches(rule.Ports, port, proto, dst.Pod) {
						r.Egress.Allowed = true
						r.Egress.AllowedBy = append(r.Egress.AllowedBy, p.Namespace+"/"+p.Name)
						break
					}
				}
			}
		}
	}
	if dst.Pod != nil {
		ps := w.Selecting(dst.Pod, networkingv1.PolicyTypeIngress)
		r.Ingress.Policies = names(ps)
		if len(ps) > 0 {
			r.Ingress.Isolated, r.Ingress.Allowed = true, false
			for _, p := range ps {
				for _, rule := range p.Spec.Ingress {
					peerOK := len(rule.From) == 0
					for _, peer := range rule.From {
						if w.peerMatches(peer, p.Namespace, src) {
							peerOK = true
							break
						}
					}
					if peerOK && portMatches(rule.Ports, port, proto, dst.Pod) {
						r.Ingress.Allowed = true
						r.Ingress.AllowedBy = append(r.Ingress.AllowedBy, p.Namespace+"/"+p.Name)
						break
					}
				}
			}
		}
	}
	r.Allowed = r.Egress.Allowed && r.Ingress.Allowed
	switch {
	case !r.Egress.Allowed:
		r.Reason = fmt.Sprintf("Bloqueado na saída: a origem é isolada para egress por %s e nenhuma regra libera este destino/porta.", strings.Join(r.Egress.Policies, ", "))
	case !r.Ingress.Allowed:
		r.Reason = fmt.Sprintf("Bloqueado na entrada: o destino é isolado para ingress por %s e nenhuma regra libera esta origem/porta.", strings.Join(r.Ingress.Policies, ", "))
	case !r.Egress.Isolated && !r.Ingress.Isolated:
		r.Reason = "Permitido: nenhuma NetworkPolicy seleciona a origem (egress) nem o destino (ingress)."
	default:
		r.Reason = "Permitido pelas regras: " + strings.Join(append(r.Egress.AllowedBy, r.Ingress.AllowedBy...), ", ") + "."
	}
	return r
}

// Group is a set of pods of one workload (or one standalone pod).
type Group struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	Pods            []string `json:"pods"`
	IngressPolicies []string `json:"ingressPolicies"`
	EgressPolicies  []string `json:"egressPolicies"`
	IngressIsolated bool     `json:"ingressIsolated"`
	EgressIsolated  bool     `json:"egressIsolated"`
}

// Flow is an allowed ingress flow into a group.
type Flow struct {
	From   string `json:"from"` // group id, or "ns:<selector>", "cidr:<cidr>", "any"
	To     string `json:"to"`
	Ports  string `json:"ports"`
	Policy string `json:"policy"`
}

// Peer is a non-group source in the flow graph (other namespaces, CIDRs, anything).
type Peer struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Analysis is the namespace view.
type Analysis struct {
	Namespace string          `json:"namespace"`
	Groups    []Group         `json:"groups"`
	Peers     []Peer          `json:"peers"`
	Flows     []Flow          `json:"flows"`
	Policies  []PolicySummary `json:"policies"`
}

// PolicySummary is a readable policy.
type PolicySummary struct {
	Name     string   `json:"name"`
	Selector string   `json:"selector"`
	Types    []string `json:"types"`
	Ingress  []string `json:"ingress"`
	Egress   []string `json:"egress"`
	Selects  []string `json:"selects"` // group ids
}

// volatileLabels differ between pods of the same workload and never drive policy selection in practice.
var volatileLabels = map[string]bool{"pod-template-hash": true, "controller-revision-hash": true, "pod-template-generation": true,
	"statefulset.kubernetes.io/pod-name": true, "apps.kubernetes.io/pod-index": true, "deployment": true, "batch.kubernetes.io/controller-uid": true,
	"controller-uid": true, "batch.kubernetes.io/job-name": true, "job-name": true}

// labelKey groups pods that every selector treats the same way: identical labels except volatile ones.
func labelKey(p *corev1.Pod) string {
	keys := make([]string, 0, len(p.Labels))
	for k, v := range p.Labels {
		if !volatileLabels[k] {
			keys = append(keys, k+"="+v)
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// groupOf names the workload of a pod from common labels and generated names.
func groupOf(p *corev1.Pod) (string, string) {
	for _, k := range []string{"app.kubernetes.io/instance", "app.kubernetes.io/name", "app", "k8s-app", "deploymentconfig", "name"} {
		if v := p.Labels[k]; v != "" {
			return k + "=" + v, v
		}
	}
	return "pod=" + p.Name, p.Name
}

func selectorString(sel *metav1.LabelSelector) string {
	if sel == nil {
		return "todos"
	}
	s := metav1.FormatLabelSelector(sel)
	if s == "<none>" || s == "" {
		return "todos"
	}
	return s
}

func portsString(ps []networkingv1.NetworkPolicyPort) string {
	if len(ps) == 0 {
		return "todas as portas"
	}
	var out []string
	for _, p := range ps {
		proto := "TCP"
		if p.Protocol != nil {
			proto = string(*p.Protocol)
		}
		switch {
		case p.Port == nil:
			out = append(out, proto)
		case p.EndPort != nil:
			out = append(out, fmt.Sprintf("%s/%s-%d", proto, p.Port.String(), *p.EndPort))
		default:
			out = append(out, proto+"/"+p.Port.String())
		}
	}
	return strings.Join(out, ", ")
}

func peerString(peer networkingv1.NetworkPolicyPeer) string {
	switch {
	case peer.IPBlock != nil:
		s := "IPs " + peer.IPBlock.CIDR
		if len(peer.IPBlock.Except) > 0 {
			s += " exceto " + strings.Join(peer.IPBlock.Except, ", ")
		}
		return s
	case peer.NamespaceSelector != nil && peer.PodSelector != nil:
		return fmt.Sprintf("pods %s em namespaces %s", selectorString(peer.PodSelector), selectorString(peer.NamespaceSelector))
	case peer.NamespaceSelector != nil:
		return "todos os pods em namespaces " + selectorString(peer.NamespaceSelector)
	default:
		return "pods " + selectorString(peer.PodSelector) + " deste namespace"
	}
}

// Analyze describes isolation and allowed ingress flows for one namespace.
func (w *World) Analyze(ns string) *Analysis {
	a := &Analysis{Namespace: ns, Groups: []Group{}, Peers: []Peer{}, Flows: []Flow{}, Policies: []PolicySummary{}}
	groups := map[string]*Group{}
	podGroup := map[string]string{}
	byLabels := map[string]string{} // labelKey -> group id
	baseCount := map[string]int{}
	var order []string
	for i := range w.Pods {
		p := &w.Pods[i]
		if p.Namespace != ns || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		lk := labelKey(p)
		id, ok := byLabels[lk]
		label := ""
		if !ok {
			base, l := groupOf(p)
			baseCount[base]++
			id, label = base, l
			if n := baseCount[base]; n > 1 {
				// Same workload label, different label set: selectors may treat them differently.
				id, label = fmt.Sprintf("%s#%d", base, n), fmt.Sprintf("%s (%d)", l, n)
			}
			byLabels[lk] = id
		}
		g, ok := groups[id]
		if !ok {
			g = &Group{ID: id, Label: label, IngressPolicies: []string{}, EgressPolicies: []string{}}
			groups[id] = g
			order = append(order, id)
			g.IngressPolicies = names(w.Selecting(p, networkingv1.PolicyTypeIngress))
			g.EgressPolicies = names(w.Selecting(p, networkingv1.PolicyTypeEgress))
			g.IngressIsolated, g.EgressIsolated = len(g.IngressPolicies) > 0, len(g.EgressPolicies) > 0
		}
		g.Pods = append(g.Pods, p.Name)
		podGroup[p.Name] = id
	}
	sort.Strings(order)
	peers := map[string]string{}
	seenFlow := map[Flow]bool{}
	addFlow := func(f Flow) {
		if !seenFlow[f] {
			seenFlow[f] = true
			a.Flows = append(a.Flows, f)
		}
	}
	for _, id := range order {
		if !groups[id].IngressIsolated {
			peers["any"] = "qualquer origem"
			addFlow(Flow{From: "any", To: id, Ports: "todas as portas", Policy: "(sem política: aberto)"})
		}
	}
	for i := range w.Policies {
		p := &w.Policies[i]
		if p.Namespace != ns {
			continue
		}
		sum := PolicySummary{Name: p.Name, Selector: selectorString(&p.Spec.PodSelector), Ingress: []string{}, Egress: []string{}, Selects: []string{}}
		for _, t := range []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress} {
			if hasType(p, t) {
				sum.Types = append(sum.Types, string(t))
			}
		}
		var targets []string
		for _, id := range order {
			pod := w.podByName(ns, groups[id].Pods[0])
			if pod != nil && selects(&p.Spec.PodSelector, pod.Labels) {
				targets = append(targets, id)
			}
		}
		sum.Selects = append(sum.Selects, targets...)
		if hasType(p, networkingv1.PolicyTypeIngress) && len(p.Spec.Ingress) == 0 {
			sum.Ingress = append(sum.Ingress, "nega toda entrada")
		}
		ingressRules := p.Spec.Ingress
		if !hasType(p, networkingv1.PolicyTypeIngress) {
			// Kubernetes ignores ingress rules of a policy that does not declare the Ingress type.
			ingressRules = nil
		}
		for _, rule := range ingressRules {
			ports := portsString(rule.Ports)
			if len(rule.From) == 0 {
				sum.Ingress = append(sum.Ingress, "de qualquer origem → "+ports)
				peers["any"] = "qualquer origem"
				for _, t := range targets {
					addFlow(Flow{From: "any", To: t, Ports: ports, Policy: p.Name})
				}
				continue
			}
			for _, peer := range rule.From {
				sum.Ingress = append(sum.Ingress, "de "+peerString(peer)+" → "+ports)
				var froms []string
				switch {
				case peer.IPBlock != nil:
					pid := "cidr:" + peer.IPBlock.CIDR
					peers[pid] = peerString(peer)
					froms = []string{pid}
				case peer.NamespaceSelector != nil:
					pid := "ns:" + selectorString(peer.NamespaceSelector) + "|" + selectorString(peer.PodSelector)
					peers[pid] = peerString(peer)
					froms = []string{pid}
					// Same-namespace groups matched by the selector also get a direct edge.
					if selects(peer.NamespaceSelector, w.nsLabels(ns)) {
						froms = append(froms, w.groupsMatching(ns, order, groups, peer.PodSelector)...)
					}
				default:
					froms = w.groupsMatching(ns, order, groups, peer.PodSelector)
				}
				for _, f := range froms {
					for _, t := range targets {
						addFlow(Flow{From: f, To: t, Ports: ports, Policy: p.Name})
					}
				}
			}
		}
		if hasType(p, networkingv1.PolicyTypeEgress) && len(p.Spec.Egress) == 0 {
			sum.Egress = append(sum.Egress, "nega toda saída")
		}
		for _, rule := range p.Spec.Egress {
			ports := portsString(rule.Ports)
			if len(rule.To) == 0 {
				sum.Egress = append(sum.Egress, "para qualquer destino → "+ports)
				continue
			}
			for _, peer := range rule.To {
				sum.Egress = append(sum.Egress, "para "+peerString(peer)+" → "+ports)
			}
		}
		a.Policies = append(a.Policies, sum)
	}
	for _, id := range order {
		a.Groups = append(a.Groups, *groups[id])
	}
	for id, label := range peers {
		a.Peers = append(a.Peers, Peer{ID: id, Label: label})
	}
	sort.Slice(a.Peers, func(i, j int) bool { return a.Peers[i].ID < a.Peers[j].ID })
	sort.Slice(a.Policies, func(i, j int) bool { return a.Policies[i].Name < a.Policies[j].Name })
	return a
}

func (w *World) podByName(ns, name string) *corev1.Pod {
	if w.index == nil {
		w.index = make(map[string]*corev1.Pod, len(w.Pods))
		for i := range w.Pods {
			w.index[w.Pods[i].Namespace+"/"+w.Pods[i].Name] = &w.Pods[i]
		}
	}
	return w.index[ns+"/"+name]
}

func (w *World) groupsMatching(ns string, order []string, groups map[string]*Group, sel *metav1.LabelSelector) []string {
	var out []string
	for _, id := range order {
		if p := w.podByName(ns, groups[id].Pods[0]); p != nil && (sel == nil || selects(sel, p.Labels)) {
			out = append(out, id)
		}
	}
	return out
}
