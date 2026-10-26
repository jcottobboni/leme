package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/helm"
)

func (s *Server) helmRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/clusters/{id}/helm/releases", s.withSession(s.helmList))
	m.HandleFunc("GET /api/clusters/{id}/helm/releases/{ns}/{name}", s.withSession(s.helmGet))
	m.HandleFunc("GET /api/clusters/{id}/helm/releases/{ns}/{name}/manifest", s.withSession(s.helmManifest))
	m.HandleFunc("POST /api/clusters/{id}/helm/releases/{ns}/{name}/upgrade", s.withSession(s.helmUpgrade))
	m.HandleFunc("POST /api/clusters/{id}/helm/releases/{ns}/{name}/rollback", s.withSession(s.helmRollback))
	m.HandleFunc("POST /api/clusters/{id}/helm/releases/{ns}/{name}/uninstall", s.withSession(s.helmUninstall))
	m.HandleFunc("POST /api/clusters/{id}/helm/install", s.withSession(s.helmInstall))
	m.HandleFunc("GET /api/helm/repos", s.helmRepos)
	m.HandleFunc("POST /api/helm/repos", s.helmAddRepo)
	m.HandleFunc("DELETE /api/helm/repos/{name}", s.helmRemoveRepo)
	m.HandleFunc("POST /api/helm/repos/update", s.helmUpdateRepos)
	m.HandleFunc("GET /api/helm/charts", s.helmSearch)
	m.HandleFunc("GET /api/helm/chart", s.helmShow)
}

// mutationContext detaches long Helm operations from the HTTP request (a closed tab must not abort an
// upgrade halfway) but ties them to the session's write scope: locking or disconnecting stops them.
func mutationContext(sess *cluster.Session) (context.Context, context.CancelFunc) {
	return context.WithTimeout(sess.WriteScope(), 6*time.Minute)
}

func (s *Server) helmList(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	rels, err := s.helm.List(sess, r.URL.Query().Get("namespace"))
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, rels)
}

func (s *Server) helmGet(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	ns, name := r.PathValue("ns"), r.PathValue("name")
	reveal := r.URL.Query().Get("reveal") == "1"
	d, err := s.helm.Get(sess, ns, name, reveal)
	if err != nil {
		kubeError(w, err)
		return
	}
	if reveal {
		s.log.Info("helm release revealed (values, notes and manifest secrets)", "context", sess.Context.Name, "namespace", ns, "release", name)
	}
	writeJSON(w, d)
}

func (s *Server) helmManifest(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	rev, _ := strconv.Atoi(r.URL.Query().Get("revision"))
	m, err := s.helm.Manifest(sess, r.PathValue("ns"), r.PathValue("name"), rev, false)
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, map[string]string{"manifest": m})
}

func (s *Server) helmUpgrade(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req helm.UpgradeRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := mutationContext(sess)
	defer cancel()
	ns, name := r.PathValue("ns"), r.PathValue("name")
	res, err := s.helm.Upgrade(ctx, sess, ns, name, req)
	if err != nil {
		kubeError(w, err)
		return
	}
	if !req.DryRun {
		s.log.Info("helm upgrade", "context", sess.Context.Name, "namespace", ns, "release", name, "revision", res.Revision, "chart", req.Chart, "version", req.Version)
	}
	writeJSON(w, res)
}

func (s *Server) helmRollback(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct {
		Revision int  `json:"revision"`
		DryRun   bool `json:"dryRun"`
	}
	if !decode(w, r, &req) {
		return
	}
	ns, name := r.PathValue("ns"), r.PathValue("name")
	res, err := s.helm.Rollback(sess, ns, name, req.Revision, req.DryRun)
	if err != nil {
		kubeError(w, err)
		return
	}
	if !req.DryRun {
		s.log.Info("helm rollback", "context", sess.Context.Name, "namespace", ns, "release", name, "to", req.Revision)
	}
	writeJSON(w, res)
}

func (s *Server) helmUninstall(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct {
		DryRun bool `json:"dryRun"`
	}
	if !decode(w, r, &req) {
		return
	}
	ns, name := r.PathValue("ns"), r.PathValue("name")
	res, err := s.helm.Uninstall(sess, ns, name, req.DryRun)
	if err != nil {
		kubeError(w, err)
		return
	}
	if !req.DryRun {
		s.log.Info("helm uninstall", "context", sess.Context.Name, "namespace", ns, "release", name)
	}
	writeJSON(w, res)
}

func (s *Server) helmInstall(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req helm.InstallRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := mutationContext(sess)
	defer cancel()
	res, err := s.helm.Install(ctx, sess, req)
	if err != nil {
		kubeError(w, err)
		return
	}
	if !req.DryRun {
		s.log.Info("helm install", "context", sess.Context.Name, "namespace", req.Namespace, "release", req.Name, "chart", req.Chart, "version", req.Version)
	}
	writeJSON(w, res)
}

func (s *Server) helmRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.helm.Repos()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, repos)
}

func (s *Server) helmAddRepo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.helm.AddRepo(req.Name, req.URL); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("helm repo added", "name", req.Name, "url", req.URL)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) helmRemoveRepo(w http.ResponseWriter, r *http.Request) {
	if err := s.helm.RemoveRepo(r.PathValue("name")); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("helm repo removed", "name", r.PathValue("name"))
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) helmUpdateRepos(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Names []string `json:"names"`
	}
	if !decode(w, r, &req) {
		return
	}
	writeJSON(w, map[string]any{"errors": s.helm.UpdateRepos(req.Names)})
}

func (s *Server) helmSearch(w http.ResponseWriter, r *http.Request) {
	hits, err := s.helm.Search(r.URL.Query().Get("q"), 200)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, hits)
}

func (s *Server) helmShow(w http.ResponseWriter, r *http.Request) {
	info, err := s.helm.Show(r.URL.Query().Get("ref"), r.URL.Query().Get("version"))
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, info)
}
