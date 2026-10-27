package api

import (
	"errors"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/netpol"
)

// NetworkPolicy analysis and connectivity checks.

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
	res := world.Check(src, dst, req.Port, corev1.Protocol(proto))
	writeJSON(w, map[string]any{"result": res, "errors": errs})
}
