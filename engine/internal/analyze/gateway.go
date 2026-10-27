package analyze

import (
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/project"
)

// gatewayResources are the Gateway API resources diagnosed by Namespace.
var gatewayResources = []string{"gateways", "httproutes", "grpcroutes", "tlsroutes", "tcproutes", "udproutes"}

// gatewayAPIFindings dispatches the Gateway API rules (gateway.networking.k8s.io only: Istio's
// networking.istio.io/Gateway never reaches here).
func (c *scope) gatewayAPIFindings(u *unstructured.Unstructured) []Finding {
	switch u.GetKind() {
	case "Gateway":
		return gatewayFindings(u)
	case "HTTPRoute", "GRPCRoute", "TLSRoute", "TCPRoute", "UDPRoute":
		return c.gwRouteFindings(u)
	}
	return nil
}

func condEvidence(prefix string, cm map[string]any) string {
	return fmt.Sprintf("%scondição %v=%v reason=%v%s", prefix, cm["type"], cm["status"], cm["reason"], msgSuffix(condStr(cm, "message")))
}

func gatewayFindings(u *unstructured.Unstructured) []Finding {
	ref := refOf(u)
	if acc, ok := cond(u.Object, "Accepted"); ok && condStr(acc, "status") == "False" {
		return []Finding{{ID: "gateway-not-accepted", Severity: High, Object: ref, Title: "Gateway não aceito pelo controlador",
			Explanation: "O controlador da GatewayClass recusou o Gateway (por exemplo, classe inexistente, listener inválido ou endereço não suportado); nenhuma rota passa por ele.",
			Evidence:    []string{"spec.gatewayClassName=" + str(u.Object, "spec", "gatewayClassName"), condEvidence("", acc)},
			Suggestion:  "Corrija o que a mensagem da condição aponta e confira se a GatewayClass existe e está aceita."}}
	}
	prog, ok := cond(u.Object, "Programmed")
	if !ok {
		return nil
	}
	switch condStr(prog, "status") {
	case "False":
		if condStr(prog, "reason") == "Pending" {
			break
		}
		return []Finding{{ID: "gateway-not-programmed", Severity: High, Object: ref, Title: "Gateway não programado",
			Explanation: "O Gateway foi aceito, mas o controlador não conseguiu configurar o data plane (proxy, load balancer ou endereço); o tráfego não chega às rotas.",
			Evidence:    []string{condEvidence("", prog)},
			Suggestion:  "Veja os eventos e os logs do controlador da GatewayClass e o motivo indicado na condição (endereço indisponível, listener inválido)."}}
	}
	if condStr(prog, "status") != "True" {
		return []Finding{{ID: "gateway-pending", Severity: Low, Object: ref, Title: "Gateway ainda não programado",
			Explanation: "Nenhum controlador marcou o Gateway como programado. Em geral não há controlador para a GatewayClass, ou ele ainda está processando.",
			Evidence:    []string{"spec.gatewayClassName=" + str(u.Object, "spec", "gatewayClassName"), condEvidence("", prog)},
			Suggestion:  "Confira se a GatewayClass existe, está aceita e se o controlador dela está rodando no cluster."}}
	}
	return nil
}

func (c *scope) gwRouteFindings(u *unstructured.Unstructured) []Finding {
	ref := refOf(u)
	ns := u.GetNamespace()
	var out []Finding
	for _, st := range project.RouteStatuses(u) {
		parent := st.Parent.String(ns)
		if st.Accepted != nil && condStr(st.Accepted, "status") == "False" {
			out = append(out, Finding{ID: "gwroute-not-accepted", Severity: High, Object: ref, Title: u.GetKind() + " não aceita pelo Gateway",
				Explanation: "O Gateway pai recusou a rota (hostname sem listener correspondente, namespace não permitido em allowedRoutes ou sectionName inexistente); ela não recebe tráfego por ele.",
				Evidence:    []string{"parent " + parent, condEvidence("", st.Accepted)},
				Suggestion:  "Compare hostnames, sectionName e namespace da rota com os listeners e allowedRoutes do Gateway."})
		}
		if st.ResolvedRefs != nil && condStr(st.ResolvedRefs, "status") == "False" {
			out = append(out, Finding{ID: "gwroute-refs-unresolved", Severity: High, Object: ref, Title: u.GetKind() + " com referências não resolvidas",
				Explanation: "O controlador não conseguiu resolver um ou mais backendRefs (Service inexistente, porta errada ou referência entre namespaces sem ReferenceGrant).",
				Evidence:    []string{"parent " + parent, condEvidence("", st.ResolvedRefs)},
				Suggestion:  "Corrija os backendRefs indicados na mensagem da condição."})
		}
	}
	for _, p := range project.RouteParents(u) {
		if p.Group != project.GatewayGroup || p.Kind != "Gateway" || p.Name == "" {
			continue
		}
		if gw, known := c.getIn(project.GatewayGroup, "gateways", p.Namespace, p.Name); known && gw == nil {
			out = append(out, Finding{ID: "gwroute-missing-gateway", Severity: High, Object: ref, Title: u.GetKind() + " aponta para Gateway inexistente",
				Explanation: "O parentRef da rota referencia um Gateway que não existe; a rota não é anexada a nada.",
				Evidence:    []string{fmt.Sprintf("spec.parentRefs: Gateway %s/%s (não encontrado)", p.Namespace, p.Name)},
				Suggestion:  "Corrija o nome ou o namespace do parentRef, ou crie o Gateway.", Fixable: p.Namespace == ns})
		}
	}
	for _, b := range project.RouteBackends(u) {
		if !b.IsService() {
			continue
		}
		svc, known := c.getIn("", "services", b.Namespace, b.Name)
		if !known {
			continue
		}
		if svc == nil {
			out = append(out, Finding{ID: "gwroute-missing-backend", Severity: High, Object: ref, Title: u.GetKind() + " aponta para Service inexistente",
				Explanation: "Um backendRef da rota referencia um Service que não existe; as requisições para esse backend falham (HTTP 500 no caso de HTTPRoute).",
				Evidence:    []string{fmt.Sprintf("backendRef Service %s/%s (não encontrado)", b.Namespace, b.Name)},
				Suggestion:  "Crie o Service ou corrija o nome e o namespace no backendRef.", Fixable: b.Namespace == ns})
			continue
		}
		if b.Port != 0 && !portMatches(servicePorts(svc), "", b.Port, "") {
			out = append(out, Finding{ID: "gwroute-missing-port", Severity: High, Object: ref, Title: u.GetKind() + " aponta para porta inexistente no Service",
				Explanation: "O Service existe, mas não expõe a porta do backendRef.",
				Evidence:    []string{fmt.Sprintf("backendRef %s/%s port=%d; portas do Service: %s", b.Namespace, b.Name, b.Port, describePorts(servicePorts(svc)))},
				Suggestion:  "Use no backendRef uma porta que o Service exponha.", Fixable: true})
		}
		if b.Namespace != ns {
			if granted, known := c.referenceGranted(ns, u.GetKind(), b); known && !granted {
				out = append(out, Finding{ID: "gwroute-no-referencegrant", Severity: High, Object: ref, Title: "Backend em outro namespace sem ReferenceGrant",
					Explanation: "Rotas só podem apontar para Services de outro namespace quando um ReferenceGrant no namespace do Service permite; sem ele o controlador recusa a referência (ResolvedRefs=False, RefNotPermitted).",
					Evidence:    []string{fmt.Sprintf("backendRef Service %s/%s a partir de %s %s/%s", b.Namespace, b.Name, u.GetKind(), ns, u.GetName()), "nenhum ReferenceGrant em " + b.Namespace + " permite " + u.GetKind() + " de " + ns},
					Suggestion:  fmt.Sprintf("Crie em %s um ReferenceGrant com from {group: gateway.networking.k8s.io, kind: %s, namespace: %s} e to {group: \"\", kind: Service}.", b.Namespace, u.GetKind(), ns)})
			}
		}
	}
	return out
}

// referenceGranted reports whether a ReferenceGrant in the backend's namespace lets routes of kind
// from namespace fromNS reference it. known is false when the grants could not be read.
func (c *scope) referenceGranted(fromNS, kind string, b project.BackendRef) (granted, known bool) {
	grants, ok := c.listIn(project.GatewayGroup, "referencegrants", b.Namespace)
	if !ok {
		return false, false
	}
	for _, g := range grants {
		fromOK := false
		for _, f := range slices(g.Object, "spec", "from") {
			if str(f, "group") == project.GatewayGroup && str(f, "kind") == kind && str(f, "namespace") == fromNS {
				fromOK = true
				break
			}
		}
		if !fromOK {
			continue
		}
		for _, t := range slices(g.Object, "spec", "to") {
			if str(t, "group") == b.Group && str(t, "kind") == b.Kind && (str(t, "name") == "" || str(t, "name") == b.Name) {
				return true, true
			}
		}
	}
	return false, true
}

func slices(o map[string]any, path ...string) []map[string]any {
	v, _, _ := unstructured.NestedFieldNoCopy(o, path...)
	l, _ := v.([]any)
	out := make([]map[string]any, 0, len(l))
	for _, it := range l {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// listIn lists a resource of another namespace than the scope's (from the scope when it covers it).
// ok is false when the resource is not served or cannot be listed.
func (c *scope) listIn(group, resource, ns string) ([]*unstructured.Unstructured, bool) {
	if c.ns == "" || c.ns == ns {
		l, err := c.list(group, resource)
		if err != nil {
			return nil, false
		}
		if c.ns == "" {
			var out []*unstructured.Unstructured
			for _, u := range l {
				if u.GetNamespace() == ns {
					out = append(out, u)
				}
			}
			return out, true
		}
		return l, true
	}
	res, ok := c.s.Lookup(group, resource)
	if !ok {
		return nil, false
	}
	l, err := c.s.Dynamic.Resource(res.GVR()).Namespace(ns).List(c.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, false
	}
	out := make([]*unstructured.Unstructured, 0, len(l.Items))
	for i := range l.Items {
		out = append(out, &l.Items[i])
	}
	return out, true
}

// getIn returns an object of any namespace: nil with known=true when it does not exist, known=false
// when it could not be checked (resource not served, no permission).
func (c *scope) getIn(group, resource, ns, name string) (*unstructured.Unstructured, bool) {
	if c.ns == "" || c.ns == ns {
		l, err := c.list(group, resource)
		if err != nil {
			return nil, false
		}
		if _, served := c.s.Lookup(group, resource); !served {
			return nil, false
		}
		for _, u := range l {
			if u.GetName() == name && u.GetNamespace() == ns {
				return u, true
			}
		}
		return nil, true
	}
	res, ok := c.s.Lookup(group, resource)
	if !ok {
		return nil, false
	}
	u, err := c.s.Dynamic.Resource(res.GVR()).Namespace(ns).Get(c.ctx, name, metav1.GetOptions{})
	switch {
	case err == nil:
		return u, true
	case apierrors.IsNotFound(err):
		return nil, true
	}
	return nil, false
}
