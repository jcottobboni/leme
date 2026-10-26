package security

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// Input is what Scan needs, already listed (keys are "group/resource").
type Input struct {
	Objects map[string][]*unstructured.Unstructured
}

// WorkloadScore summarizes one workload.
type WorkloadScore struct {
	Group     string         `json:"group"`
	Kind      string         `json:"kind"`
	Namespace string         `json:"namespace,omitempty"`
	Name      string         `json:"name"`
	Score     int            `json:"score"`
	Counts    map[string]int `json:"counts"`
}

// Report is the posture of a namespace or of the cluster.
type Report struct {
	Scope                   string            `json:"scope"`
	Score                   int               `json:"score"`
	Counts                  map[string]int    `json:"counts"`
	ByCategory              map[string]int    `json:"byCategory"`
	Findings                []Finding         `json:"findings"`
	Workloads               []WorkloadScore   `json:"workloads"`
	NamespacesWithoutNetpol []string          `json:"namespacesWithoutNetpol"`
	Errors                  map[string]string `json:"errors,omitempty"`
}

var workloadKeys = []string{"/pods", "apps/deployments", "apps/statefulsets", "apps/daemonsets", "apps/replicasets", "batch/jobs", "batch/cronjobs", "/replicationcontrollers", "apps.openshift.io/deploymentconfigs"}

// ownerKey identifies a controller as "Kind/ns/name".
func ownerKey(kind, ns, name string) string { return kind + "/" + ns + "/" + name }

// topOwners maps each pod to its top-level controller (Deployment, CronJob, DeploymentConfig…).
func topOwners(in Input) map[string]string {
	parent := map[string]string{}
	for _, key := range []string{"apps/replicasets", "batch/jobs", "/replicationcontrollers"} {
		for _, u := range in.Objects[key] {
			for _, o := range u.GetOwnerReferences() {
				if o.Controller != nil && *o.Controller {
					parent[ownerKey(u.GetKind(), u.GetNamespace(), u.GetName())] = ownerKey(o.Kind, u.GetNamespace(), o.Name)
				}
			}
		}
	}
	out := map[string]string{}
	for _, p := range in.Objects["/pods"] {
		for _, o := range p.GetOwnerReferences() {
			if o.Controller == nil || !*o.Controller {
				continue
			}
			k := ownerKey(o.Kind, p.GetNamespace(), o.Name)
			for i := 0; i < 3; i++ {
				up, ok := parent[k]
				if !ok {
					break
				}
				k = up
			}
			if _, seen := out[k]; !seen && p.GetDeletionTimestamp() == nil {
				out[k] = ownerKey("Pod", p.GetNamespace(), p.GetName())
			}
		}
	}
	return out
}

func controlled(u *unstructured.Unstructured) bool {
	for _, o := range u.GetOwnerReferences() {
		if o.Controller != nil && *o.Controller {
			return true
		}
	}
	return false
}

// Scan evaluates every top-level workload (pods owned by a controller are evaluated through it,
// using one running pod as the effective spec when available) plus namespace-level rules.
func Scan(in Input, scope string) *Report {
	r := &Report{Scope: scope, Counts: map[string]int{}, ByCategory: map[string]int{}, Findings: []Finding{}, Workloads: []WorkloadScore{}, NamespacesWithoutNetpol: []string{}}
	pods := map[string]*unstructured.Unstructured{}
	for _, p := range in.Objects["/pods"] {
		pods[ownerKey("Pod", p.GetNamespace(), p.GetName())] = p
	}
	live := topOwners(in)
	for _, key := range workloadKeys {
		for _, u := range in.Objects[key] {
			// Intermediate controllers (ReplicaSet of a Deployment, Job of a CronJob, RC of a DC) and
			// controlled pods are covered by their owner.
			if controlled(u) {
				continue
			}
			ref := RefOf(u)
			spec, ok := PodSpec(u)
			effective := false
			if pk, ok2 := live[ownerKey(u.GetKind(), u.GetNamespace(), u.GetName())]; ok2 && u.GetKind() != "Pod" {
				if p := pods[pk]; p != nil {
					if ps, ok3 := PodSpec(p); ok3 {
						spec, ok, effective = ps, true, true
					}
				}
			}
			if !ok {
				continue
			}
			fs := CheckPodSpec(ref, spec, effective)
			ws := WorkloadScore{Group: ref.Group, Kind: ref.Kind, Namespace: ref.Namespace, Name: ref.Name, Score: Score(fs), Counts: map[string]int{}}
			for _, f := range fs {
				ws.Counts[f.Severity]++
			}
			r.Workloads = append(r.Workloads, ws)
			r.Findings = append(r.Findings, fs...)
		}
	}
	r.Findings = append(r.Findings, namespaceFindings(in, r)...)
	for _, f := range r.Findings {
		r.Counts[f.Severity]++
		r.ByCategory[f.Category]++
	}
	if len(r.Workloads) == 0 {
		r.Score = 100
	} else {
		sum := 0
		for _, w := range r.Workloads {
			sum += w.Score
		}
		r.Score = sum / len(r.Workloads)
	}
	// Namespaces with open pods lose points too: missing network policies matter beyond one workload.
	r.Score -= min(20, 5*len(r.NamespacesWithoutNetpol))
	if r.Score < 0 {
		r.Score = 0
	}
	sort.SliceStable(r.Workloads, func(i, j int) bool {
		if r.Workloads[i].Score != r.Workloads[j].Score {
			return r.Workloads[i].Score < r.Workloads[j].Score
		}
		return r.Workloads[i].Namespace+r.Workloads[i].Name < r.Workloads[j].Namespace+r.Workloads[j].Name
	})
	rank := map[string]int{Critical: 0, High: 1, Medium: 2, Low: 3, Info: 4}
	sort.SliceStable(r.Findings, func(i, j int) bool { return rank[r.Findings[i].Severity] < rank[r.Findings[j].Severity] })
	return r
}

// systemNamespace reports control-plane namespaces where missing policies are expected noise.
func systemNamespace(ns string) bool {
	return ns == "kube-system" || ns == "kube-public" || ns == "kube-node-lease" || strings.HasPrefix(ns, "openshift") || ns == "local-path-storage"
}

func namespaceFindings(in Input, r *Report) []Finding {
	podNS := map[string]bool{}
	for _, p := range in.Objects["/pods"] {
		if ph, _, _ := unstructured.NestedString(p.Object, "status", "phase"); ph == "Running" || ph == "Pending" {
			podNS[p.GetNamespace()] = true
		}
	}
	has := func(key string) map[string]bool {
		m := map[string]bool{}
		for _, u := range in.Objects[key] {
			m[u.GetNamespace()] = true
		}
		return m
	}
	netpol, quota, limits := has("networking.k8s.io/networkpolicies"), has("/resourcequotas"), has("/limitranges")
	var out []Finding
	var nss []string
	for ns := range podNS {
		nss = append(nss, ns)
	}
	sort.Strings(nss)
	for _, ns := range nss {
		if systemNamespace(ns) {
			continue
		}
		ref := Finding{Kind: "Namespace", Name: ns, Source: "leme"}
		if !netpol[ns] {
			r.NamespacesWithoutNetpol = append(r.NamespacesWithoutNetpol, ns)
			f := ref
			f.ID, f.Severity, f.Category, f.Title = "no-network-policy", Medium, CatNetwork, "Namespace sem NetworkPolicy"
			f.Detail = "Todos os pods aceitam tráfego de qualquer pod do cluster e podem sair para qualquer destino."
			f.Remediation = "Crie uma política default-deny de ingress e libere só os fluxos necessários (veja a tela NetworkPolicy)."
			out = append(out, f)
		}
		if !quota[ns] && !limits[ns] {
			f := ref
			f.ID, f.Severity, f.Category, f.Title = "no-quota", Info, CatResources, "Namespace sem ResourceQuota nem LimitRange"
			f.Detail = "Nada limita o consumo total do namespace nem define defaults para pods sem requests."
			f.Remediation = "Defina uma ResourceQuota e um LimitRange com defaults."
			out = append(out, f)
		}
	}
	return out
}

var collectKeys = append(append([]string{}, workloadKeys...), "networking.k8s.io/networkpolicies", "/resourcequotas", "/limitranges",
	"/services", "networking.k8s.io/ingresses", "/serviceaccounts", "rbac.authorization.k8s.io/roles", "rbac.authorization.k8s.io/rolebindings")

// Collect lists the scan input from the session caches. ns "" scans the whole cluster.
func Collect(ctx context.Context, sess *cluster.Session, ns string) (Input, map[string]string) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	in := Input{Objects: map[string][]*unstructured.Unstructured{}}
	errs := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, key := range collectKeys {
		group, resource, _ := strings.Cut(key, "/")
		res, ok := sess.Lookup(group, resource)
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			objs, err := sess.Informers.List(ctx, cluster.Key{GVR: res.GVR(), Namespace: ns})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[key] = err.Error()
				return
			}
			in.Objects[key] = objs
		}()
	}
	wg.Wait()
	return in, errs
}
