// Package argocd reads Argo CD Applications and AppProjects through the cluster's own API (the
// user's kubeconfig; no Argo CD credentials or API server) and builds the only two changes Leme
// may propose on them: a sync operation and a refresh annotation. The patches are built here,
// from the current object, and never come from the UI, an AI or an agent.
package argocd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// Group is the API group of the Argo CD CRDs.
const Group = "argoproj.io"

// Initiator is the user name recorded in operation.initiatedBy.
const Initiator = "leme"

// RefreshAnnotation asks the Argo CD controller to re-read the application's source.
const RefreshAnnotation = "argocd.argoproj.io/refresh"

// Hint explains what is missing when the CRDs are not installed.
const Hint = "Os CRDs do Argo CD (applications.argoproj.io e appprojects.argoproj.io) não existem neste cluster. " +
	"Instale o Argo CD no cluster (ou conecte o cluster onde ele roda) para ver as Applications aqui. " +
	"O Leme lê as Applications pela API do Kubernetes, com o seu kubeconfig: não precisa de login no Argo CD."

// ErrOperationRunning refuses a sync while another operation is in progress.
var ErrOperationRunning = errors.New("uma operação do Argo CD já está em andamento nesta Application: aguarde terminar")

// ErrOperationPending refuses a sync while a requested operation was not picked up yet.
var ErrOperationPending = errors.New("já existe uma operação pedida nesta Application que o controlador ainda não iniciou")

// Allow is the change policy for this feature: only argoproj.io/Application may be modified.
func Allow(group, kind string) error {
	if group == Group && kind == "Application" {
		return nil
	}
	return fmt.Errorf("the Argo CD integration only changes %s/Application, not %s/%s", Group, group, kind)
}

// Source is where an Application's manifests come from.
type Source struct {
	RepoURL        string `json:"repoURL"`
	Path           string `json:"path,omitempty"`
	TargetRevision string `json:"targetRevision,omitempty"`
	Chart          string `json:"chart,omitempty"`
}

// Destination is where an Application deploys.
type Destination struct {
	Server    string `json:"server,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
}

// App summarizes an Application.
type App struct {
	Namespace      string      `json:"namespace"`
	Name           string      `json:"name"`
	Project        string      `json:"project"`
	Sync           string      `json:"sync"`
	Health         string      `json:"health"`
	Source         *Source     `json:"source,omitempty"`
	Sources        []Source    `json:"sources,omitempty"`
	Destination    Destination `json:"destination"`
	Revision       string      `json:"revision,omitempty"`
	OperationPhase string      `json:"operationPhase,omitempty"`
	Automated      bool        `json:"automated,omitempty"`
	Local          bool        `json:"local"` // destination is the cluster Leme is connected to
}

// Project summarizes an AppProject.
type Project struct {
	Namespace    string        `json:"namespace"`
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	SourceRepos  []string      `json:"sourceRepos,omitempty"`
	Destinations []Destination `json:"destinations,omitempty"`
}

// Overview is the list response.
type Overview struct {
	Installed bool              `json:"installed"`
	Apps      []App             `json:"apps"`
	Projects  []Project         `json:"projects"`
	Hint      string            `json:"hint,omitempty"`
	Errors    map[string]string `json:"errors,omitempty"`
}

// Resource is one object managed by an Application, as Argo CD reports it.
type Resource struct {
	Group     string `json:"group"`
	Version   string `json:"version,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Status    string `json:"status,omitempty"`
	Health    string `json:"health,omitempty"`
	Message   string `json:"message,omitempty"`
	// Exists is whether the object is in the connected cluster; nil when not checked (remote
	// destination, unknown kind or too many objects).
	Exists *bool `json:"exists,omitempty"`
}

// Condition is an Application condition.
type Condition struct {
	Type               string `json:"type"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// History is one deployed revision.
type History struct {
	ID         int64    `json:"id"`
	Revision   string   `json:"revision,omitempty"`
	Revisions  []string `json:"revisions,omitempty"`
	DeployedAt string   `json:"deployedAt,omitempty"`
	Source     *Source  `json:"source,omitempty"`
	Initiator  string   `json:"initiatedBy,omitempty"`
}

// OperationState is the last (or current) operation.
type OperationState struct {
	Phase      string `json:"phase,omitempty"`
	Message    string `json:"message,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	Revision   string `json:"revision,omitempty"`
	Initiator  string `json:"initiatedBy,omitempty"`
}

// Detail is an Application with its resources, conditions and history.
type Detail struct {
	App
	Resources        []Resource      `json:"resources"`
	Conditions       []Condition     `json:"conditions"`
	History          []History       `json:"history"`
	OperationState   *OperationState `json:"operationState,omitempty"`
	PendingOperation bool            `json:"pendingOperation,omitempty"`
	HealthMessage    string          `json:"healthMessage,omitempty"`
	ReconciledAt     string          `json:"reconciledAt,omitempty"`
	Checked          bool            `json:"checked"` // resources were looked up in the connected cluster
}

// Kinds returns the Application and AppProject resources, when the CRDs are installed.
func Kinds(s *cluster.Session) (app, proj cluster.Resource, ok bool) {
	app, okA := s.LookupKind(Group, "Application")
	proj, okP := s.LookupKind(Group, "AppProject")
	return app, proj, okA && okP
}

// List returns the Applications (all namespaces when ns is empty) and every AppProject.
func List(ctx context.Context, s *cluster.Session, ns string) (*Overview, error) {
	appRes, projRes, ok := Kinds(s)
	if !ok {
		return &Overview{Installed: false, Apps: []App{}, Projects: []Project{}, Hint: Hint}, nil
	}
	out := &Overview{Installed: true, Apps: []App{}, Projects: []Project{}}
	list, err := s.Dynamic.Resource(appRes.GVR()).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range list.Items {
		out.Apps = append(out.Apps, ParseApp(&list.Items[i]))
	}
	sort.Slice(out.Apps, func(i, j int) bool {
		a, b := out.Apps[i], out.Apps[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	// Projects are context only: losing them (RBAC) must not hide the Applications.
	if pl, err := s.Dynamic.Resource(projRes.GVR()).Namespace("").List(ctx, metav1.ListOptions{}); err != nil {
		out.Errors = map[string]string{"appprojects": err.Error()}
	} else {
		for i := range pl.Items {
			out.Projects = append(out.Projects, parseProject(&pl.Items[i]))
		}
		sort.Slice(out.Projects, func(i, j int) bool { return out.Projects[i].Name < out.Projects[j].Name })
	}
	return out, nil
}

// Get returns the raw Application object.
func Get(ctx context.Context, s *cluster.Session, ns, name string) (*unstructured.Unstructured, error) {
	appRes, _, ok := Kinds(s)
	if !ok {
		return nil, errors.New(Hint)
	}
	return s.Dynamic.Resource(appRes.GVR()).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
}

// maxChecked caps how many managed objects are looked up in the connected cluster.
const maxChecked = 300

// Describe returns the detail of an Application; managed resources are checked against the
// connected cluster when the destination is local.
func Describe(ctx context.Context, s *cluster.Session, ns, name string) (*Detail, error) {
	u, err := Get(ctx, s, ns, name)
	if err != nil {
		return nil, err
	}
	d := ParseDetail(u)
	if d.Local && len(d.Resources) <= maxChecked {
		d.Checked = true
		for i := range d.Resources {
			d.Resources[i].Exists = exists(ctx, s, d.Resources[i])
		}
	}
	return d, nil
}

func exists(ctx context.Context, s *cluster.Session, r Resource) *bool {
	res, ok := s.LookupKind(r.Group, r.Kind)
	if !ok {
		return nil
	}
	ns := r.Namespace
	if !res.Namespaced {
		ns = ""
	}
	_, err := s.Dynamic.Resource(res.GVR()).Namespace(ns).Get(ctx, r.Name, metav1.GetOptions{})
	switch {
	case err == nil:
		v := true
		return &v
	case apierrors.IsNotFound(err):
		v := false
		return &v
	default:
		return nil // forbidden or unreachable: unknown, not "missing"
	}
}

// ParseApp reads the summary fields of an Application.
func ParseApp(u *unstructured.Unstructured) App {
	o := u.Object
	a := App{
		Namespace: u.GetNamespace(), Name: u.GetName(),
		Project:        str(o, "spec", "project"),
		Sync:           orUnknown(str(o, "status", "sync", "status")),
		Health:         orUnknown(str(o, "status", "health", "status")),
		Revision:       str(o, "status", "sync", "revision"),
		OperationPhase: str(o, "status", "operationState", "phase"),
		Destination: Destination{
			Server: str(o, "spec", "destination", "server"), Namespace: str(o, "spec", "destination", "namespace"),
			Name: str(o, "spec", "destination", "name"),
		},
	}
	if m, ok := mapAt(o, "spec", "source"); ok {
		src := parseSource(m)
		a.Source = &src
	}
	if l, ok, _ := unstructured.NestedSlice(o, "spec", "sources"); ok {
		for _, it := range l {
			if m, ok := it.(map[string]any); ok {
				a.Sources = append(a.Sources, parseSource(m))
			}
		}
	}
	if a.Revision == "" {
		if revs, ok, _ := unstructured.NestedStringSlice(o, "status", "sync", "revisions"); ok && len(revs) > 0 {
			a.Revision = revs[0]
		}
	}
	if _, ok := mapAt(o, "spec", "syncPolicy", "automated"); ok {
		a.Automated = true
	}
	a.Local = isLocal(a.Destination)
	return a
}

// isLocal reports whether the destination is the cluster the Application lives in (Argo CD's
// "in-cluster"), which is the cluster Leme is connected to.
func isLocal(d Destination) bool {
	if d.Server != "" {
		return strings.TrimSuffix(d.Server, "/") == "https://kubernetes.default.svc"
	}
	return d.Name == "in-cluster"
}

// ParseDetail reads the detail of an Application (without the existence check).
func ParseDetail(u *unstructured.Unstructured) *Detail {
	o := u.Object
	d := &Detail{App: ParseApp(u), Resources: []Resource{}, Conditions: []Condition{}, History: []History{}}
	d.HealthMessage = str(o, "status", "health", "message")
	d.ReconciledAt = str(o, "status", "reconciledAt")
	_, d.PendingOperation = mapAt(o, "operation")
	for _, m := range maps(o, "status", "resources") {
		d.Resources = append(d.Resources, Resource{
			Group: str(m, "group"), Version: str(m, "version"), Kind: str(m, "kind"), Namespace: str(m, "namespace"),
			Name: str(m, "name"), Status: str(m, "status"), Health: str(m, "health", "status"), Message: str(m, "health", "message"),
		})
	}
	sort.SliceStable(d.Resources, func(i, j int) bool {
		a, b := d.Resources[i], d.Resources[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	for _, m := range maps(o, "status", "conditions") {
		d.Conditions = append(d.Conditions, Condition{Type: str(m, "type"), Message: str(m, "message"), LastTransitionTime: str(m, "lastTransitionTime")})
	}
	for _, m := range maps(o, "status", "history") {
		h := History{Revision: str(m, "revision"), DeployedAt: str(m, "deployedAt"), Initiator: str(m, "initiatedBy", "username")}
		h.ID, _, _ = unstructured.NestedInt64(m, "id")
		if h.ID == 0 {
			if f, ok, _ := unstructured.NestedFloat64(m, "id"); ok {
				h.ID = int64(f)
			}
		}
		h.Revisions, _, _ = unstructured.NestedStringSlice(m, "revisions")
		if s, ok := mapAt(m, "source"); ok {
			src := parseSource(s)
			h.Source = &src
		}
		d.History = append(d.History, h)
	}
	// Newest first.
	sort.SliceStable(d.History, func(i, j int) bool { return d.History[i].ID > d.History[j].ID })
	if m, ok := mapAt(o, "status", "operationState"); ok {
		d.OperationState = &OperationState{
			Phase: str(m, "phase"), Message: str(m, "message"), StartedAt: str(m, "startedAt"), FinishedAt: str(m, "finishedAt"),
			Revision:  firstNonEmpty(str(m, "syncResult", "revision"), str(m, "operation", "sync", "revision")),
			Initiator: str(m, "operation", "initiatedBy", "username"),
		}
	}
	return d
}

func parseProject(u *unstructured.Unstructured) Project {
	o := u.Object
	p := Project{Namespace: u.GetNamespace(), Name: u.GetName(), Description: str(o, "spec", "description")}
	p.SourceRepos, _, _ = unstructured.NestedStringSlice(o, "spec", "sourceRepos")
	for _, m := range maps(o, "spec", "destinations") {
		p.Destinations = append(p.Destinations, Destination{Server: str(m, "server"), Namespace: str(m, "namespace"), Name: str(m, "name")})
	}
	return p
}

func parseSource(m map[string]any) Source {
	return Source{RepoURL: str(m, "repoURL"), Path: str(m, "path"), TargetRevision: str(m, "targetRevision"), Chart: str(m, "chart")}
}

// SyncPatch builds the merge patch that asks the Argo CD controller to sync the Application to
// the revision it already compares against (status.sync.revision, or the source's
// targetRevision). It is refused while an operation is running or still pending.
func SyncPatch(u *unstructured.Unstructured, prune bool) (map[string]any, error) {
	o := u.Object
	if strings.EqualFold(str(o, "status", "operationState", "phase"), "Running") {
		return nil, ErrOperationRunning
	}
	if _, pending := mapAt(o, "operation"); pending {
		return nil, ErrOperationPending
	}
	sync := map[string]any{"prune": prune}
	if l, ok, _ := unstructured.NestedSlice(o, "spec", "sources"); ok && len(l) > 0 {
		// Multi-source Application: one revision per source, in order.
		revs, _, _ := unstructured.NestedStringSlice(o, "status", "sync", "revisions")
		if len(revs) != len(l) {
			revs = make([]string, 0, len(l))
			for _, it := range l {
				m, _ := it.(map[string]any)
				revs = append(revs, firstNonEmpty(str(m, "targetRevision"), "HEAD"))
			}
		}
		sync["revisions"] = toAny(revs)
	} else if rev := firstNonEmpty(str(o, "status", "sync", "revision"), str(o, "spec", "source", "targetRevision")); rev != "" {
		sync["revision"] = rev
	}
	return map[string]any{"operation": map[string]any{
		"initiatedBy": map[string]any{"username": Initiator},
		"sync":        sync,
	}}, nil
}

// RefreshPatch builds the merge patch that asks for a normal or hard refresh.
func RefreshPatch(hard bool) map[string]any {
	v := "normal"
	if hard {
		v = "hard"
	}
	return map[string]any{"metadata": map[string]any{"annotations": map[string]any{RefreshAnnotation: v}}}
}

// --- unstructured helpers ---

func str(o map[string]any, path ...string) string {
	v, ok, _ := unstructured.NestedFieldNoCopy(o, path...)
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func mapAt(o map[string]any, path ...string) (map[string]any, bool) {
	v, ok, _ := unstructured.NestedFieldNoCopy(o, path...)
	if !ok {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

func maps(o map[string]any, path ...string) []map[string]any {
	v, ok, _ := unstructured.NestedFieldNoCopy(o, path...)
	if !ok {
		return nil
	}
	l, _ := v.([]any)
	out := make([]map[string]any, 0, len(l))
	for _, it := range l {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func orUnknown(s string) string {
	if s == "" {
		return "Unknown"
	}
	return s
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func toAny(l []string) []any {
	out := make([]any, len(l))
	for i, s := range l {
		out[i] = s
	}
	return out
}
