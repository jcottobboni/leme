package netpol

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func pod(ns, name, ip string, l map[string]string, ports ...corev1.ContainerPort) corev1.Pod {
	return corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: l},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Ports: ports}}},
		Status: corev1.PodStatus{PodIP: ip, Phase: corev1.PodRunning}}
}

func port(p int32) *intstr.IntOrString { v := intstr.FromInt32(p); return &v }

func world() *World {
	tcp := corev1.ProtocolTCP
	return &World{
		Namespaces: map[string]map[string]string{"demo": {"team": "a"}, "mon": {"team": "ops"}, "other": {}},
		Pods: []corev1.Pod{
			pod("demo", "web-1", "10.0.0.1", map[string]string{"app": "web"}, corev1.ContainerPort{Name: "http", ContainerPort: 80}),
			pod("demo", "client-1", "10.0.0.2", map[string]string{"app": "client"}),
			pod("demo", "broken-1", "10.0.0.3", map[string]string{"app": "broken"}),
			pod("mon", "prom-1", "10.0.1.1", map[string]string{"app": "prometheus"}),
			pod("other", "x-1", "10.0.2.1", map[string]string{"app": "client"}),
		},
		Policies: []networkingv1.NetworkPolicy{
			{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "deny-all"}, Spec: networkingv1.NetworkPolicySpec{PolicyTypes: []networkingv1.PolicyType{"Ingress"}}},
			{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "allow-client-web"}, Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
				Ingress: []networkingv1.NetworkPolicyIngressRule{
					{From: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "client"}}}},
						Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &intstr.IntOrString{Type: intstr.String, StrVal: "http"}}}},
					{From: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "ops"}}}},
						Ports: []networkingv1.NetworkPolicyPort{{Port: port(9100), EndPort: func() *int32 { v := int32(9110); return &v }()}}},
					{From: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "192.168.0.0/16", Except: []string{"192.168.1.0/24"}}}}},
				},
			}},
			{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "client-egress"}, Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "client"}},
				PolicyTypes: []networkingv1.PolicyType{"Egress"},
				Egress:      []networkingv1.NetworkPolicyEgressRule{{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}}}}},
			}},
		},
	}
}

func ep(w *World, ns, name string) Endpoint {
	for i := range w.Pods {
		if w.Pods[i].Namespace == ns && w.Pods[i].Name == name {
			return Endpoint{Pod: &w.Pods[i]}
		}
	}
	panic(name)
}

func TestCheck(t *testing.T) {
	w := world()
	cases := []struct {
		name     string
		src, dst Endpoint
		port     int32
		want     bool
	}{
		{"client → web:80 via named port", ep(w, "demo", "client-1"), ep(w, "demo", "web-1"), 80, true},
		{"client → web:443 wrong port", ep(w, "demo", "client-1"), ep(w, "demo", "web-1"), 443, false},
		{"broken → web:80 not selected", ep(w, "demo", "broken-1"), ep(w, "demo", "web-1"), 80, false},
		{"client in other ns → web (podSelector is same-namespace only)", ep(w, "other", "x-1"), ep(w, "demo", "web-1"), 80, false},
		{"prometheus ns by label → web:9105 (port range)", ep(w, "mon", "prom-1"), ep(w, "demo", "web-1"), 9105, true},
		{"prometheus → web:9200 out of range", ep(w, "mon", "prom-1"), ep(w, "demo", "web-1"), 9200, false},
		{"ipBlock allowed", Endpoint{IP: "192.168.5.5"}, ep(w, "demo", "web-1"), 1234, true},
		{"ipBlock except", Endpoint{IP: "192.168.1.5"}, ep(w, "demo", "web-1"), 1234, false},
		{"client egress only to web", ep(w, "demo", "client-1"), ep(w, "mon", "prom-1"), 9090, false},
		{"unprotected namespace", ep(w, "demo", "web-1"), ep(w, "other", "x-1"), 80, true},
		{"deny-all ingress on broken", ep(w, "demo", "web-1"), ep(w, "demo", "broken-1"), 80, false},
	}
	for _, c := range cases {
		r := w.Check(c.src, c.dst, c.port, corev1.ProtocolTCP)
		if r.Allowed != c.want {
			t.Errorf("%s: got %v (%s)", c.name, r.Allowed, r.Reason)
		}
	}
}

func TestAnalyze(t *testing.T) {
	a := world().Analyze("demo")
	if len(a.Groups) != 3 {
		t.Fatalf("groups: %+v", a.Groups)
	}
	for _, g := range a.Groups {
		if !g.IngressIsolated {
			t.Errorf("deny-all isolates every pod in demo: %+v", g)
		}
	}
	found := false
	for _, f := range a.Flows {
		if f.From == "app=client" && f.To == "app=web" && f.Policy == "allow-client-web" {
			found = true
		}
		if f.From == "any" {
			t.Errorf("no group is open, unexpected flow %+v", f)
		}
	}
	if !found {
		t.Errorf("client → web flow missing: %+v", a.Flows)
	}
	if len(a.Policies) != 3 || a.Policies[2].Name != "deny-all" || a.Policies[2].Ingress[0] != "nega toda entrada" {
		t.Errorf("policies: %+v", a.Policies)
	}
	open := world()
	open.Policies = nil
	b := open.Analyze("demo")
	if len(b.Flows) != 3 || b.Flows[0].From != "any" {
		t.Errorf("without policies every group is open: %+v", b.Flows)
	}
}

func TestAnalyzeSplitsDifferentLabelSets(t *testing.T) {
	w := world()
	w.Pods = append(w.Pods, pod("demo", "web-db", "10.0.0.9", map[string]string{"app": "web", "tier": "db"}))
	w.Policies = []networkingv1.NetworkPolicy{{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "db-only"},
		Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"tier": "db"}}}}}
	a := w.Analyze("demo")
	isolated := map[string]bool{}
	for _, g := range a.Groups {
		isolated[g.ID] = g.IngressIsolated
	}
	if isolated["app=web"] || !isolated["app=web#2"] {
		t.Fatalf("pods with the same app label but different selectors must be separate groups: %+v", a.Groups)
	}
}

func TestEgressOnlyPolicyHasNoIngressFlows(t *testing.T) {
	w := world()
	w.Policies = []networkingv1.NetworkPolicy{{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "egress-only"},
		Spec: networkingv1.NetworkPolicySpec{PolicyTypes: []networkingv1.PolicyType{"Egress"},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}}}}}}
	for _, f := range w.Analyze("demo").Flows {
		if f.Policy == "egress-only" {
			t.Fatalf("ingress rules of an Egress-only policy are ignored by Kubernetes: %+v", f)
		}
	}
}
