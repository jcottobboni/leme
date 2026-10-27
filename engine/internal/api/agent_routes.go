package api

import (
	"net/http"
	"os"

	"github.com/jcottobboni/leme/engine/internal/audit"
	"github.com/jcottobboni/leme/engine/internal/settings"
)

// agentRoutes serves the MCP agent access settings (M4, group G2). The MCP server is a separate
// process (`leme mcp`) that re-reads settings.json on every call, so what is saved here applies
// to running agents immediately.
func (s *Server) agentRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/agents/access", s.agentAccess)
	m.HandleFunc("PUT /api/agents/access/{id}", s.setAgentAccess)
}

type agentAccessView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Prod          bool   `json:"prod"`
	Write         bool   `json:"write"`
	ProdConfirmed bool   `json:"prodConfirmed"`
}

func (s *Server) agentAccess(w http.ResponseWriter, r *http.Request) {
	ctxs, err := s.mgr.Loader().Contexts()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]agentAccessView, 0, len(ctxs))
	for _, c := range ctxs {
		a := s.settings.AgentAccess(c.Name)
		ok := a.Granted(c.Fingerprint())
		out = append(out, agentAccessView{ID: c.ID, Name: c.Name, Prod: c.Prod, Write: ok, ProdConfirmed: ok && a.ProdConfirmed})
	}
	exe, _ := os.Executable()
	writeJSON(w, map[string]any{"contexts": out, "enginePath": exe})
}

func (s *Server) setAgentAccess(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Write       bool   `json:"write"`
		ConfirmName string `json:"confirmName"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctxs, err := s.mgr.Loader().Contexts()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := r.PathValue("id")
	for _, c := range ctxs {
		if c.ID != id {
			continue
		}
		// Same rule as unlocking production writes in the app: type the exact context name.
		if body.Write && c.Prod && body.ConfirmName != c.Name {
			_ = s.audit.Add(audit.Entry{Source: "app", Action: "agent-access", Context: c.Name, Result: "denied", Detail: "production confirmation missing"})
			httpError(w, http.StatusForbidden, "para produção, digite o nome exato do contexto")
			return
		}
		a := settings.AgentAccess{Write: body.Write, ProdConfirmed: body.Write && c.Prod, Fingerprint: c.Fingerprint()}
		if err := s.settings.SetAgentAccess(c.Name, a); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		detail := "agent writes disabled"
		if body.Write {
			detail = "agent writes enabled"
		}
		_ = s.audit.Add(audit.Entry{Source: "app", Action: "agent-access", Context: c.Name, Result: "ok", Detail: detail})
		writeJSON(w, agentAccessView{ID: c.ID, Name: c.Name, Prod: c.Prod, Write: a.Write, ProdConfirmed: a.ProdConfirmed})
		return
	}
	httpError(w, http.StatusNotFound, "unknown context")
}
