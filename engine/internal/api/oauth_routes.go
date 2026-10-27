package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

// OpenShift login (OAuth): password and browser flows, logout.

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
