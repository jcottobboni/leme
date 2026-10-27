package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kinds"
	"github.com/jcottobboni/leme/engine/internal/security"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Security posture, per-object findings, external scanners and image scans.

func (s *Server) securityReport(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	ns := r.URL.Query().Get("namespace")
	in, errs := security.Collect(r.Context(), sess, ns)
	scope := ns
	if scope == "" {
		scope = "cluster"
	}
	rep := security.Scan(in, scope)
	if len(errs) > 0 {
		rep.Errors = errs
	}
	writeJSON(w, rep)
}

// securityObject scans one object. The optional group query parameter selects the API group; without
// it the built-in workload kind of that name is meant (older clients send only the kind).
func (s *Server) securityObject(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	ns, group, kind, name := q.Get("namespace"), q.Get("group"), q.Get("kind"), q.Get("name")
	if group == "" {
		group, _ = kinds.WorkloadGroup(kind)
	}
	if !kinds.IsWorkload(group, kind) {
		writeJSON(w, map[string]any{"findings": []security.Finding{}, "score": 100, "applicable": false})
		return
	}
	in, errs := security.Collect(r.Context(), sess, ns)
	rep := security.Scan(in, ns)
	out := []security.Finding{}
	for _, f := range rep.Findings {
		if f.Group == group && f.Kind == kind && f.Name == name && f.Namespace == ns {
			out = append(out, f)
		}
	}
	// A controlled object (e.g. a pod of a Deployment) is scanned through its owner; show its own spec too.
	if len(out) == 0 {
		for _, u := range in.Objects {
			for _, o := range u {
				if kinds.GroupOf(o.GetAPIVersion()) == group && o.GetKind() == kind && o.GetName() == name && o.GetNamespace() == ns {
					if spec, ok := security.PodSpec(o); ok {
						out = security.CheckPodSpec(security.RefOf(o), spec, kind == "Pod")
					}
				}
			}
		}
	}
	writeJSON(w, map[string]any{"findings": out, "score": security.Score(out), "applicable": true, "errors": errs})
}

func (s *Server) securityRBAC(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	snap := s.m2.rbacCache.Get(r.Context(), sess.Typed, "", r.URL.Query().Get("refresh") == "1")
	writeJSON(w, map[string]any{"risks": security.RBACRisks(snap), "errors": snap.Errors})
}

func (s *Server) securityScan(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct {
		Tool      string `json:"tool"`
		Framework string `json:"framework"`
		Namespace string `json:"namespace"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Tool != "polaris" && req.Tool != "kubescape" {
		httpError(w, http.StatusBadRequest, "tool must be polaris or kubescape")
		return
	}
	in, _ := security.Collect(r.Context(), sess, req.Namespace)
	dir, err := security.Export(in)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer os.RemoveAll(dir)
	var fs []security.Finding
	if req.Tool == "polaris" {
		fs, err = security.RunPolaris(r.Context(), dir, s.m3.ksHome)
	} else {
		if req.Framework == "" {
			req.Framework = "nsa"
		}
		fs, err = security.RunKubescape(r.Context(), dir, s.m3.ksHome, req.Framework)
	}
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"findings": fs})
}

func workloadImages(ctx context.Context, sess *cluster.Session, group, kind, ns, name string) ([]map[string]string, error) {
	res, ok := sess.LookupKind(group, kind)
	if !ok {
		return nil, errors.New("unknown kind")
	}
	u, err := sess.Dynamic.Resource(res.GVR()).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	spec, ok := security.PodSpec(u)
	if !ok {
		return nil, errors.New("not a workload")
	}
	var out []map[string]string
	seen := map[string]bool{}
	for _, field := range []string{"initContainers", "containers"} {
		cs, _ := spec[field].([]any)
		for _, c := range cs {
			cm, _ := c.(map[string]any)
			img, _ := cm["image"].(string)
			cn, _ := cm["name"].(string)
			if img != "" && !seen[img] {
				seen[img] = true
				out = append(out, map[string]string{"image": img, "container": cn})
			}
		}
	}
	return out, nil
}

func (s *Server) securityImages(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	images, err := workloadImages(r.Context(), sess, q.Get("group"), q.Get("kind"), q.Get("namespace"), q.Get("name"))
	if err != nil {
		kubeError(w, err)
		return
	}
	out := map[string]any{"images": images, "operator": false}
	if reps, err := security.OperatorReports(r.Context(), sess, q.Get("kind"), q.Get("namespace"), q.Get("name")); err == nil {
		out["operator"], out["reports"] = true, reps
	} else if !security.IsNoOperator(err) {
		out["operatorError"] = err.Error()
	}
	writeJSON(w, out)
}

func (s *Server) securityImageScan(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct {
		Images  []string `json:"images"`
		Refresh bool     `json:"refresh"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Images) == 0 || len(req.Images) > 10 {
		httpError(w, http.StatusBadRequest, "send 1 to 10 images")
		return
	}
	// Scans survive a closed tab only as long as the session lives.
	ctx, cancel := within(r.Context(), sess.Life())
	defer cancel()
	reps := make([]security.ImageReport, len(req.Images))
	var wg sync.WaitGroup
	for i, img := range req.Images {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reps[i] = s.m3.trivy.Scan(ctx, img, req.Refresh)
		}()
	}
	wg.Wait()
	writeJSON(w, reps)
}
