package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
	"github.com/jcottobboni/leme/engine/internal/netpol"
	"github.com/jcottobboni/leme/engine/internal/ops"
	"github.com/jcottobboni/leme/engine/internal/security"
)

// m3 holds the per-server state of the M3 features.
type m3 struct {
	trivy  *security.Trivy
	ksHome string
	webMu  sync.Mutex
	web    map[string]*kube.WebLogin // pending browser logins per context id
}

func newM3(configDir string) *m3 {
	if configDir == "" {
		configDir = filepath.Join(os.TempDir(), "leme")
	}
	return &m3{trivy: security.NewTrivy(filepath.Join(configDir, "trivy")), ksHome: filepath.Join(configDir, "kubescape"), web: map[string]*kube.WebLogin{}}
}

func (s *Server) m3Routes(m *http.ServeMux) {
	m.HandleFunc("GET /api/security/tools", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, security.Tools()) })
	m.HandleFunc("GET /api/clusters/{id}/security/report", s.withSession(s.securityReport))
	m.HandleFunc("GET /api/clusters/{id}/security/object", s.withSession(s.securityObject))
	m.HandleFunc("GET /api/clusters/{id}/security/rbac", s.withSession(s.securityRBAC))
	m.HandleFunc("POST /api/clusters/{id}/security/scan", s.withSession(s.securityScan))
	m.HandleFunc("GET /api/clusters/{id}/security/images", s.withSession(s.securityImages))
	m.HandleFunc("POST /api/clusters/{id}/security/images/scan", s.withSession(s.securityImageScan))
	m.HandleFunc("GET /api/clusters/{id}/netpol", s.withSession(s.netpolAnalyze))
	m.HandleFunc("POST /api/clusters/{id}/netpol/check", s.withSession(s.netpolCheck))
	m.HandleFunc("POST /api/clusters/{id}/openshift/rollout", s.withSession(s.osRollout))
	m.HandleFunc("POST /api/clusters/{id}/openshift/build", s.withSession(s.osStartBuild))
	m.HandleFunc("GET /api/clusters/{id}/openshift/buildlog", s.withSession(s.osBuildLog))
	m.HandleFunc("GET /api/clusters/{id}/oauth", s.oauthInfo)
	m.HandleFunc("POST /api/clusters/{id}/oauth/password", s.oauthPassword)
	m.HandleFunc("POST /api/clusters/{id}/oauth/web/start", s.oauthWebStart)
	m.HandleFunc("POST /api/clusters/{id}/oauth/web/wait", s.oauthWebWait)
	m.HandleFunc("POST /api/clusters/{id}/oauth/web/cancel", s.oauthWebCancel)
	m.HandleFunc("DELETE /api/clusters/{id}/oauth", s.oauthLogout)
}

// --- Security ---

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

func (s *Server) securityObject(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	ns, kind, name := q.Get("namespace"), q.Get("kind"), q.Get("name")
	if !security.IsWorkloadKind(kind) {
		writeJSON(w, map[string]any{"findings": []security.Finding{}, "score": 100, "applicable": false})
		return
	}
	in, errs := security.Collect(r.Context(), sess, ns)
	rep := security.Scan(in, ns)
	out := []security.Finding{}
	for _, f := range rep.Findings {
		if f.Kind == kind && f.Name == name && f.Namespace == ns {
			out = append(out, f)
		}
	}
	// A controlled object (e.g. a pod of a Deployment) is scanned through its owner; show its own spec too.
	if len(out) == 0 {
		for _, u := range in.Objects {
			for _, o := range u {
				if o.GetKind() == kind && o.GetName() == name && o.GetNamespace() == ns {
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
	u, err := sess.Dynamic.Resource(res.GVR()).Namespace(ns).Get(ctx, name, metaGet())
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

// --- NetworkPolicy ---

func (s *Server) netpolAnalyze(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		httpError(w, http.StatusBadRequest, "namespace is required")
		return
	}
	world, errs := netpol.Load(r.Context(), sess, ns)
	writeJSON(w, map[string]any{"analysis": world.Analyze(ns), "errors": errs})
}

type endpointReq struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	IP        string `json:"ip"`
}

func (s *Server) netpolCheck(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct {
		Src      endpointReq `json:"src"`
		Dst      endpointReq `json:"dst"`
		Port     int32       `json:"port"`
		Protocol string      `json:"protocol"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Port <= 0 || req.Port > 65535 {
		httpError(w, http.StatusBadRequest, "port must be 1-65535")
		return
	}
	world, errs := netpol.Load(r.Context(), sess, req.Src.Namespace, req.Dst.Namespace)
	resolve := func(e endpointReq) (netpol.Endpoint, error) {
		if e.Pod == "" {
			if e.IP == "" {
				return netpol.Endpoint{}, errors.New("informe um pod ou um IP")
			}
			return netpol.Endpoint{IP: e.IP}, nil
		}
		p := world.Find(e.Namespace, e.Pod)
		if p == nil {
			return netpol.Endpoint{}, errors.New("pod " + e.Namespace + "/" + e.Pod + " não encontrado")
		}
		return netpol.Endpoint{Pod: p}, nil
	}
	src, err := resolve(req.Src)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	dst, err := resolve(req.Dst)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	proto := strings.ToUpper(req.Protocol)
	if proto == "" {
		proto = "TCP"
	}
	res := world.Check(src, dst, req.Port, corev1Protocol(proto))
	writeJSON(w, map[string]any{"result": res, "errors": errs})
}

// --- OpenShift ---

func (s *Server) osRollout(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct{ Namespace, Name string }
	if !decode(w, r, &req) {
		return
	}
	v, err := ops.RolloutLatest(r.Context(), sess, req.Namespace, req.Name)
	if err != nil {
		kubeError(w, err)
		return
	}
	s.log.Info("openshift rollout latest", "context", sess.Context.Name, "namespace", req.Namespace, "dc", req.Name, "revision", v)
	writeJSON(w, map[string]int64{"revision": v})
}

func (s *Server) osStartBuild(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct{ Namespace, Name string }
	if !decode(w, r, &req) {
		return
	}
	b, err := ops.StartBuild(r.Context(), sess, req.Namespace, req.Name)
	if err != nil {
		kubeError(w, err)
		return
	}
	s.log.Info("openshift start build", "context", sess.Context.Name, "namespace", req.Namespace, "bc", req.Name, "build", b)
	writeJSON(w, map[string]string{"build": b})
}

func (s *Server) osBuildLog(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, done := within(c.CloseRead(r.Context()), sess.Life())
	defer done()
	if err := ops.StreamBuildLogs(ctx, sess, q.Get("namespace"), q.Get("name"), q.Get("follow") != "0", wsWriter{ctx, c, websocket.MessageText}); err != nil {
		_ = c.Write(ctx, websocket.MessageText, []byte("\r\n[Leme] "+err.Error()+"\r\n"))
	}
	c.Close(websocket.StatusNormalClosure, "")
}

// --- OpenShift login (OAuth) ---

func (s *Server) oauthInfo(w http.ResponseWriter, r *http.Request) {
	_, cfg, err := s.mgr.Resolve(r.PathValue("id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	srv, err := kube.DiscoverOAuth(ctx, cfg)
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"available": true, "issuer": srv.Issuer, "inAppToken": s.mgr.HasToken(r.PathValue("id")), "insecure": cfg.Insecure})
}

// adoptToken validates a new token (who am I) and makes the next connection use it.
func (s *Server) adoptToken(ctx context.Context, id string, kc kube.Context, token string, w http.ResponseWriter) {
	_, cfg, err := s.mgr.Resolve(id)
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	user, err := whoAmI(ctx, cluster.WithToken(cfg, token))
	if err != nil {
		httpError(w, http.StatusBadGateway, "o token obtido não foi aceito pelo API server: "+err.Error())
		return
	}
	s.forwards.StopCluster(id)
	s.mgr.SetToken(id, token)
	s.log.Info("openshift login", "context", kc.Name, "user", user)
	writeJSON(w, map[string]string{"user": user})
}

func (s *Server) oauthPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kc, cfg, err := s.mgr.Resolve(id)
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Username == "" || req.Password == "" {
		httpError(w, http.StatusBadRequest, "usuário e senha são obrigatórios")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	srv, err := kube.DiscoverOAuth(ctx, cfg)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	token, err := kube.PasswordLogin(ctx, cfg, srv, req.Username, req.Password)
	req.Password = ""
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, kube.ErrBadCredentials) {
			code = http.StatusUnauthorized
		}
		httpError(w, code, err.Error())
		return
	}
	s.adoptToken(ctx, id, kc, token, w)
}

func (s *Server) oauthWebStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, cfg, err := s.mgr.Resolve(id)
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	srv, err := kube.DiscoverOAuth(ctx, cfg)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	wl, err := kube.StartWebLogin(cfg, srv)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.m3.webMu.Lock()
	if old := s.m3.web[id]; old != nil {
		old.Close()
	}
	s.m3.web[id] = wl
	s.m3.webMu.Unlock()
	writeJSON(w, map[string]string{"url": wl.AuthorizeURL})
}

func (s *Server) oauthWebWait(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kc, _, err := s.mgr.Resolve(id)
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	s.m3.webMu.Lock()
	wl := s.m3.web[id]
	s.m3.webMu.Unlock()
	if wl == nil {
		httpError(w, http.StatusConflict, "nenhum login pelo navegador em andamento")
		return
	}
	// The entry stays registered while waiting so a new start or a cancel can close this listener.
	defer func() {
		s.m3.webMu.Lock()
		if s.m3.web[id] == wl {
			delete(s.m3.web, id)
		}
		s.m3.webMu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(r.Context(), kube.WebLoginTTL)
	defer cancel()
	token, err := wl.Wait(ctx)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.adoptToken(ctx, id, kc, token, w)
}

func (s *Server) oauthWebCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.m3.webMu.Lock()
	if wl := s.m3.web[id]; wl != nil {
		wl.Close()
		delete(s.m3.web, id)
	}
	s.m3.webMu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

// oauthLogout drops the in-app login: the next connection uses the kubeconfig credentials again.
func (s *Server) oauthLogout(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.forwards.StopCluster(id)
	s.mgr.ClearToken(id)
	s.mgr.Disconnect(id)
	writeJSON(w, map[string]bool{"ok": true})
}

// whoAmI asks OpenShift who the token belongs to (users/~), which also proves the token works.
func whoAmI(ctx context.Context, cfg *rest.Config) (string, error) {
	c, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return "", err
	}
	raw, err := c.Discovery().RESTClient().Get().AbsPath("/apis/user.openshift.io/v1/users/~").Do(ctx).Raw()
	if err != nil {
		return "", err
	}
	var u struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return "", err
	}
	return u.Metadata.Name, nil
}

func metaGet() metav1.GetOptions { return metav1.GetOptions{} }

func corev1Protocol(p string) corev1.Protocol { return corev1.Protocol(p) }
