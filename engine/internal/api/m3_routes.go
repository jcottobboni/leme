package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/jcottobboni/leme/engine/internal/kube"
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
