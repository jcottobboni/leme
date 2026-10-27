package analyze

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
)

func (c *scope) serviceByName(ns, name string) *unstructured.Unstructured {
	for _, s := range c.mustList("", "services") {
		if s.GetName() == name && s.GetNamespace() == ns {
			return s
		}
	}
	return nil
}

type svcPort struct {
	name       string
	port       int64
	targetPort string
}

func servicePorts(s *unstructured.Unstructured) []svcPort {
	raw, _, _ := unstructured.NestedFieldNoCopy(s.Object, "spec", "ports")
	list, _ := raw.([]any)
	var out []svcPort
	for _, it := range list {
		m, _ := it.(map[string]any)
		out = append(out, svcPort{name: fmt.Sprint(m["name"]), port: num(m, "port"), targetPort: fmt.Sprint(m["targetPort"])})
	}
	return out
}

func (c *scope) serviceFindings(s *unstructured.Unstructured) []Finding {
	sel, _, _ := unstructured.NestedStringMap(s.Object, "spec", "selector")
	if len(sel) == 0 || str(s.Object, "spec", "type") == "ExternalName" {
		return nil
	}
	set := labels.SelectorFromSet(sel)
	total, ready := 0, 0
	for p, l := range c.podLabels() {
		if p.GetNamespace() != s.GetNamespace() || !set.Matches(l) {
			continue
		}
		total++
		if cd, ok := cond(p.Object, "Ready"); ok && condStr(cd, "status") == "True" {
			ready++
		}
	}
	ref := refOf(s)
	selector := "spec.selector=" + set.String()
	switch {
	case total == 0:
		return []Finding{{ID: "service-no-pods", Severity: High, Object: ref, Title: "Service sem pods correspondentes",
			Explanation: "O selector do Service não casa com nenhum pod, então ele não tem endpoints e o tráfego falha.",
			Evidence:    []string{selector, "pods correspondentes: 0"},
			Suggestion:  "Compare o selector do Service com os labels dos pods do workload e corrija o que estiver divergente.", Fixable: true}}
	case ready == 0:
		return []Finding{{ID: "service-no-endpoints", Severity: High, Object: ref, Title: "Service sem endpoints prontos",
			Explanation: "Os pods correspondem ao selector, mas nenhum está pronto; o Service não tem endpoints.",
			Evidence:    []string{selector, fmt.Sprintf("pods correspondentes: %d, prontos: 0", total)},
			Suggestion:  "Veja os problemas dos pods (readiness, crash, imagem) que este Service seleciona."}}
	}
	return nil
}

func (c *scope) ingressFindings(u *unstructured.Unstructured) []Finding {
	ns := u.GetNamespace()
	type backend struct {
		svc  string
		port string
		num  int64
	}
	var backends []backend
	collect := func(m map[string]any) {
		svc := str(m, "service", "name")
		if svc == "" {
			return
		}
		backends = append(backends, backend{svc, str(m, "service", "port", "name"), num(m, "service", "port", "number")})
	}
	if m, ok, _ := unstructured.NestedMap(u.Object, "spec", "defaultBackend"); ok {
		collect(m)
	}
	rules, _, _ := unstructured.NestedFieldNoCopy(u.Object, "spec", "rules")
	rl, _ := rules.([]any)
	for _, r := range rl {
		paths, _, _ := unstructured.NestedFieldNoCopy(r.(map[string]any), "http", "paths")
		pl, _ := paths.([]any)
		for _, p := range pl {
			if b, ok := p.(map[string]any)["backend"].(map[string]any); ok {
				collect(b)
			}
		}
	}
	ref := refOf(u)
	var out []Finding
	seen := map[string]bool{}
	for _, b := range backends {
		id := b.svc + "/" + b.port + fmt.Sprint(b.num)
		if seen[id] {
			continue
		}
		seen[id] = true
		svc := c.serviceByName(ns, b.svc)
		if svc == nil {
			out = append(out, Finding{ID: "ingress-missing-service", Severity: High, Object: ref, Title: "Ingress aponta para Service inexistente",
				Explanation: "O backend do Ingress referencia um Service que não existe no namespace; o controlador responde com erro.",
				Evidence:    []string{fmt.Sprintf("backend.service.name=%s (não encontrado em %s)", b.svc, ns)},
				Suggestion:  "Crie o Service ou corrija o nome do backend no Ingress.", Fixable: true})
			continue
		}
		if !portMatches(servicePorts(svc), b.port, b.num, "") {
			out = append(out, Finding{ID: "ingress-missing-port", Severity: High, Object: ref, Title: "Ingress aponta para porta inexistente no Service",
				Explanation: "O Service existe, mas não expõe a porta referenciada pelo Ingress.",
				Evidence:    []string{fmt.Sprintf("backend %s porta nome=%q número=%d; portas do Service: %s", b.svc, b.port, b.num, describePorts(servicePorts(svc)))},
				Suggestion:  "Use no Ingress uma porta (nome ou número) que o Service exponha, ou adicione a porta ao Service.", Fixable: true})
		}
	}
	return out
}

func (c *scope) routeFindings(u *unstructured.Unstructured) []Finding {
	ns := u.GetNamespace()
	ref := refOf(u)
	var out []Finding
	names := []string{}
	if n := str(u.Object, "spec", "to", "name"); n != "" && str(u.Object, "spec", "to", "kind") != "ServiceImport" {
		names = append(names, n)
	}
	alt, _, _ := unstructured.NestedFieldNoCopy(u.Object, "spec", "alternateBackends")
	al, _ := alt.([]any)
	for _, a := range al {
		if m, ok := a.(map[string]any); ok {
			names = append(names, fmt.Sprint(m["name"]))
		}
	}
	tp, _, _ := unstructured.NestedFieldNoCopy(u.Object, "spec", "port", "targetPort")
	for i, n := range names {
		svc := c.serviceByName(ns, n)
		if svc == nil {
			out = append(out, Finding{ID: "route-missing-service", Severity: High, Object: ref, Title: "Route aponta para Service inexistente",
				Explanation: "O Route referencia um Service que não existe no projeto; o roteador responde 503.",
				Evidence:    []string{fmt.Sprintf("spec.to.name=%s (não encontrado em %s)", n, ns)},
				Suggestion:  "Crie o Service ou corrija o nome em spec.to do Route.", Fixable: true})
			continue
		}
		if i == 0 && tp != nil {
			name, number := "", int64(0)
			switch v := tp.(type) {
			case string:
				name = v
			default:
				number = num(map[string]any{"v": v}, "v")
			}
			if !portMatches(servicePorts(svc), name, number, name) {
				out = append(out, Finding{ID: "route-missing-port", Severity: High, Object: ref, Title: "Route aponta para porta inexistente no Service",
					Explanation: "O targetPort do Route não corresponde a nenhuma porta do Service.",
					Evidence:    []string{fmt.Sprintf("spec.port.targetPort=%v; portas do Service %s: %s", tp, n, describePorts(servicePorts(svc)))},
					Suggestion:  "Ajuste spec.port.targetPort para o nome ou a porta de destino de uma porta do Service.", Fixable: true})
			}
		}
	}
	ing, _, _ := unstructured.NestedFieldNoCopy(u.Object, "status", "ingress")
	il, _ := ing.([]any)
	for _, it := range il {
		m, _ := it.(map[string]any)
		cs, _ := m["conditions"].([]any)
		for _, cc := range cs {
			cm, _ := cc.(map[string]any)
			if cm["type"] == "Admitted" && cm["status"] == "False" {
				out = append(out, Finding{ID: "route-not-admitted", Severity: Medium, Object: ref, Title: "Route não admitido pelo roteador",
					Explanation: "O roteador rejeitou o Route (por exemplo, host em conflito ou sem permissão).",
					Evidence:    []string{fmt.Sprintf("condição Admitted=False reason=%v%s", cm["reason"], msgSuffix(fmt.Sprint(cm["message"])))},
					Suggestion:  "Corrija o conflito indicado na mensagem (host duplicado, domínio não permitido, certificado)."})
				return out
			}
		}
	}
	return out
}

// portMatches checks a backend port (by name, number or target) against the Service ports.
func portMatches(ports []svcPort, name string, number int64, target string) bool {
	for _, p := range ports {
		if name != "" && (p.name == name) {
			return true
		}
		if target != "" && p.targetPort == target {
			return true
		}
		if number != 0 && (p.port == number || p.targetPort == fmt.Sprint(number)) {
			return true
		}
	}
	return name == "" && number == 0
}

func describePorts(ports []svcPort) string {
	var s []string
	for _, p := range ports {
		s = append(s, fmt.Sprintf("%s:%d->%s", p.name, p.port, p.targetPort))
	}
	if len(s) == 0 {
		return "nenhuma"
	}
	return strings.Join(s, ", ")
}
