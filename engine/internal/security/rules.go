// Package security evaluates workload, namespace and RBAC posture with deterministic rules and
// normalizes results of optional external scanners.
package security

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jcottobboni/leme/engine/internal/kinds"
)

// Severities, most severe first.
const (
	Critical = "critical"
	High     = "high"
	Medium   = "medium"
	Low      = "low"
	Info     = "info"
)

// Categories.
const (
	CatPrivileges  = "Privilégios"
	CatHost        = "Isolamento do host"
	CatImages      = "Imagens"
	CatResources   = "Recursos"
	CatReliability = "Confiabilidade"
	CatNetwork     = "Rede"
	CatAccess      = "Acesso (RBAC)"
)

// Weight of each severity in the 0–100 score.
var Weight = map[string]int{Critical: 30, High: 15, Medium: 6, Low: 2, Info: 0}

// Finding is one problem on one object (or container).
type Finding struct {
	ID          string `json:"id"`
	Severity    string `json:"severity"`
	Category    string `json:"category"`
	Title       string `json:"title"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
	Group       string `json:"group"`
	Kind        string `json:"kind"`
	Namespace   string `json:"namespace,omitempty"`
	Name        string `json:"name"`
	Container   string `json:"container,omitempty"`
	Source      string `json:"source"` // leme, polaris, kubescape, trivy
	// Effective is set when the rule was evaluated on a running pod (after admission, e.g. OpenShift SCC).
	Effective bool `json:"effective,omitempty"`
}

// PodSpec extracts the pod spec of a workload (or pod) of a built-in group.
func PodSpec(u *unstructured.Unstructured) (map[string]any, bool) {
	p := kinds.PodTemplatePath(kinds.GroupOf(u.GetAPIVersion()), u.GetKind())
	if p == nil {
		return nil, false
	}
	m, ok, _ := unstructured.NestedMap(u.Object, p...)
	return m, ok
}

func boolAt(m map[string]any, path ...string) (bool, bool) {
	v, ok, _ := unstructured.NestedFieldNoCopy(m, path...)
	b, isBool := v.(bool)
	return b, ok && isBool
}

func intAt(m map[string]any, path ...string) (int64, bool) {
	v, ok, _ := unstructured.NestedFieldNoCopy(m, path...)
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	}
	return 0, false
}

func strAt(m map[string]any, path ...string) string {
	s, _, _ := unstructured.NestedString(m, path...)
	return s
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// dangerousCaps can be used to escape the container or take over the node.
var dangerousCaps = map[string]bool{"ALL": true, "SYS_ADMIN": true, "NET_ADMIN": true, "SYS_PTRACE": true, "SYS_MODULE": true, "DAC_READ_SEARCH": true, "NET_RAW": false, "BPF": true, "PERFMON": true, "SYS_RAWIO": true}

// sensitivePaths on the host give node or runtime control when mounted.
var sensitivePaths = []string{"/var/run/docker.sock", "/run/containerd", "/var/run/crio", "/var/lib/kubelet", "/etc/kubernetes", "/proc", "/sys", "/root"}

type checker struct {
	ref       Finding
	effective bool
	out       []Finding
}

func (c *checker) add(id, sev, cat, container, title, detail, fix string) {
	f := c.ref
	f.ID, f.Severity, f.Category, f.Container, f.Title, f.Detail, f.Remediation, f.Source, f.Effective = id, sev, cat, container, title, detail, fix, "leme", c.effective
	c.out = append(c.out, f)
}

// CheckPodSpec evaluates a pod spec. ref carries the object identity copied into each finding.
func CheckPodSpec(ref Finding, spec map[string]any, effective bool) []Finding {
	c := &checker{ref: ref, effective: effective}
	podSC := asMap(spec["securityContext"])

	if b, _ := boolAt(spec, "hostNetwork"); b {
		c.add("host-network", High, CatHost, "", "Usa a rede do host (hostNetwork)", "O pod enxerga todas as interfaces do node e pode escutar tráfego de outros pods.", "Remova hostNetwork: true, a menos que seja um agente de rede do node.")
	}
	if b, _ := boolAt(spec, "hostPID"); b {
		c.add("host-pid", High, CatHost, "", "Compartilha processos do host (hostPID)", "Permite ver e sinalizar processos do node, inclusive de outros containers.", "Remova hostPID: true.")
	}
	if b, _ := boolAt(spec, "hostIPC"); b {
		c.add("host-ipc", High, CatHost, "", "Compartilha IPC do host (hostIPC)", "Acesso à memória compartilhada de processos do node.", "Remova hostIPC: true.")
	}
	vols, _, _ := unstructured.NestedSlice(spec, "volumes")
	for _, v := range vols {
		vm := asMap(v)
		p := strAt(vm, "hostPath", "path")
		if p == "" {
			continue
		}
		sev := High
		for _, s := range sensitivePaths {
			if p == "/" || p == s || strings.HasPrefix(p, s+"/") {
				sev = Critical
			}
		}
		c.add("host-path", sev, CatHost, "", fmt.Sprintf("Monta caminho do host (%s)", p), "Volumes hostPath dão acesso ao sistema de arquivos do node; caminhos de runtime ou kubelet permitem tomar o node.", "Use PVC, ConfigMap, Secret ou emptyDir. Se for indispensável, monte readOnly e o caminho mais restrito possível.")
	}
	if b, ok := boolAt(spec, "automountServiceAccountToken"); !ok || b {
		c.add("sa-token-automount", Low, CatPrivileges, "", "Token da ServiceAccount montado automaticamente", "Se o app não fala com o API server, o token só serve a um atacante que comprometa o container.", "Defina automountServiceAccountToken: false no pod (ou na ServiceAccount).")
	}
	podNonRoot, _ := boolAt(podSC, "runAsNonRoot")
	podUser, podUserSet := intAt(podSC, "runAsUser")
	podSeccomp := strAt(podSC, "seccompProfile", "type")

	for _, field := range []string{"initContainers", "containers"} {
		cs, _, _ := unstructured.NestedSlice(spec, field)
		for _, raw := range cs {
			ct := asMap(raw)
			name := strAt(ct, "name")
			sc := asMap(ct["securityContext"])
			if b, _ := boolAt(sc, "privileged"); b {
				c.add("privileged", Critical, CatPrivileges, name, "Container privilegiado", "Um container privilegiado tem praticamente os mesmos poderes de root no node.", "Remova privileged: true; conceda só as capabilities necessárias.")
			}
			if b, ok := boolAt(sc, "allowPrivilegeEscalation"); !ok || b {
				priv, _ := boolAt(sc, "privileged")
				if !priv {
					c.add("privilege-escalation", Medium, CatPrivileges, name, "Escalada de privilégio permitida", "Sem allowPrivilegeEscalation: false, binários setuid podem ganhar mais privilégios que o processo pai.", "Defina securityContext.allowPrivilegeEscalation: false.")
				}
			}
			user, userSet := intAt(sc, "runAsUser")
			if !userSet {
				user, userSet = podUser, podUserSet
			}
			nonRoot, nonRootSet := boolAt(sc, "runAsNonRoot")
			if !nonRootSet {
				nonRoot = podNonRoot
			}
			switch {
			case userSet && user == 0:
				c.add("run-as-root", High, CatPrivileges, name, "Roda explicitamente como root (runAsUser: 0)", "Um escape do container começa com root.", "Use um UID não zero e runAsNonRoot: true.")
			case !nonRoot && !(userSet && user > 0):
				c.add("run-as-non-root", Medium, CatPrivileges, name, "Pode rodar como root", "Nem runAsNonRoot nem um runAsUser não zero estão definidos; a imagem decide o usuário.", "Defina runAsNonRoot: true (e um runAsUser) no pod ou no container.")
			}
			if b, _ := boolAt(sc, "readOnlyRootFilesystem"); !b {
				c.add("read-only-root-fs", Low, CatPrivileges, name, "Sistema de arquivos raiz gravável", "Um invasor pode alterar binários ou deixar ferramentas no container.", "Defina readOnlyRootFilesystem: true e use emptyDir para diretórios graváveis.")
			}
			adds, _, _ := unstructured.NestedStringSlice(sc, "capabilities", "add")
			for _, cp := range adds {
				cp = strings.TrimPrefix(strings.ToUpper(cp), "CAP_")
				if dangerousCaps[cp] {
					c.add("dangerous-capability", High, CatPrivileges, name, fmt.Sprintf("Capability perigosa: %s", cp), "Essa capability permite escapar do container ou interferir no node.", "Remova a capability; prefira drop: [ALL] e adicionar só o mínimo (ex.: NET_BIND_SERVICE).")
				}
			}
			drops, _, _ := unstructured.NestedStringSlice(sc, "capabilities", "drop")
			dropAll := false
			for _, d := range drops {
				if strings.EqualFold(d, "ALL") {
					dropAll = true
				}
			}
			if !dropAll {
				c.add("capabilities-not-dropped", Low, CatPrivileges, name, "Capabilities padrão não removidas", "O runtime concede um conjunto padrão de capabilities que a maioria dos apps não usa.", "Adicione capabilities.drop: [ALL].")
			}
			seccomp := strAt(sc, "seccompProfile", "type")
			if seccomp == "" {
				seccomp = podSeccomp
			}
			if seccomp != "RuntimeDefault" && seccomp != "Localhost" {
				c.add("seccomp", Low, CatPrivileges, name, "Sem perfil seccomp", "Sem seccomp o container pode usar qualquer syscall do kernel.", "Defina seccompProfile.type: RuntimeDefault no pod.")
			}
			ports, _, _ := unstructured.NestedSlice(ct, "ports")
			for _, p := range ports {
				if hp, ok := intAt(asMap(p), "hostPort"); ok && hp > 0 {
					c.add("host-port", Medium, CatHost, name, fmt.Sprintf("Expõe hostPort %d", hp), "hostPort abre a porta direto no node e limita o agendamento.", "Use um Service em vez de hostPort.")
				}
			}
			image := strAt(ct, "image")
			if imageUnpinned(image) {
				c.add("image-tag", Medium, CatImages, name, fmt.Sprintf("Imagem sem versão fixa (%s)", image), "Tag latest ou ausente muda sem aviso e impede reproduzir ou auditar o que roda.", "Use uma tag de versão ou, melhor, um digest (@sha256:…).")
			}
			if field == "initContainers" {
				continue
			}
			if _, ok, _ := unstructured.NestedMap(ct, "resources", "requests"); !ok {
				c.add("requests-missing", Low, CatResources, name, "Sem requests de CPU e memória", "O scheduler não reserva recursos; o pod pode ser despejado primeiro sob pressão.", "Defina resources.requests de cpu e memory.")
			}
			if _, ok, _ := unstructured.NestedString(ct, "resources", "limits", "memory"); !ok {
				c.add("memory-limit-missing", Low, CatResources, name, "Sem limite de memória", "Um vazamento de memória pode afetar outros pods do node.", "Defina resources.limits.memory.")
			}
			if _, ok := ct["readinessProbe"]; !ok {
				c.add("readiness-probe", Low, CatReliability, name, "Sem readinessProbe", "O Service manda tráfego para o pod antes de ele estar pronto.", "Adicione uma readinessProbe.")
			}
			if _, ok := ct["livenessProbe"]; !ok {
				c.add("liveness-probe", Info, CatReliability, name, "Sem livenessProbe", "Travamentos não são detectados pelo kubelet.", "Considere uma livenessProbe (cuidado com reinícios em cascata).")
			}
		}
	}
	return c.out
}

func imageUnpinned(image string) bool {
	if image == "" || strings.Contains(image, "@sha256:") {
		return false
	}
	last := image[strings.LastIndex(image, "/")+1:]
	i := strings.LastIndex(last, ":")
	return i < 0 || last[i+1:] == "latest"
}

// RefOf builds the identity part of a finding.
func RefOf(u *unstructured.Unstructured) Finding {
	gv, _ := schema.ParseGroupVersion(u.GetAPIVersion())
	return Finding{Group: gv.Group, Kind: u.GetKind(), Namespace: u.GetNamespace(), Name: u.GetName()}
}

// Score turns findings into 0–100 (100 = nothing found).
func Score(fs []Finding) int {
	s := 100
	for _, f := range fs {
		s -= Weight[f.Severity]
	}
	if s < 0 {
		return 0
	}
	return s
}
