package api

import (
	"errors"
	"net/http"

	"github.com/jcottobboni/leme/engine/internal/argocd"
	"github.com/jcottobboni/leme/engine/internal/audit"
	"github.com/jcottobboni/leme/engine/internal/change"
	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// argocdRoutes serves Argo CD Applications, sync and refresh proposals (M5, feature argocd).
// Everything goes through the Kubernetes API with the user's kubeconfig: no Argo CD credentials.
func (s *Server) argocdRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/clusters/{id}/argocd", s.withSession(s.argoList))
	m.HandleFunc("GET /api/clusters/{id}/argocd/apps/{ns}/{name}", s.withSession(s.argoDetail))
	m.HandleFunc("POST /api/clusters/{id}/argocd/apps/{ns}/{name}/propose", s.withSession(s.argoPropose))
	m.HandleFunc("POST /api/clusters/{id}/argocd/apply/{token}", s.withSession(s.argoApply))
}

// argoSource tags proposals and audit entries of this feature.
const argoSource = "argocd"

func (s *Server) argoList(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	ov, err := argocd.List(r.Context(), sess, r.URL.Query().Get("namespace"))
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, ov)
}

func (s *Server) argoDetail(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	if _, _, ok := argocd.Kinds(sess); !ok {
		httpError(w, http.StatusNotFound, argocd.Hint)
		return
	}
	d, err := argocd.Describe(r.Context(), sess, r.PathValue("ns"), r.PathValue("name"))
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, d)
}

type argoProposeRequest struct {
	Action string `json:"action"` // sync | refresh
	Hard   bool   `json:"hard,omitempty"`
	Prune  bool   `json:"prune,omitempty"`
}

// argoProposal is the change proposal plus notes specific to Argo CD (e.g. what prune means).
type argoProposal struct {
	change.Proposal
	Notes []string `json:"notes,omitempty"`
}

func (s *Server) argoPropose(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req argoProposeRequest
	if !decode(w, r, &req) {
		return
	}
	ns, name := r.PathValue("ns"), r.PathValue("name")
	if _, _, ok := argocd.Kinds(sess); !ok {
		httpError(w, http.StatusNotFound, argocd.Hint)
		return
	}
	ref := change.Ref{Group: argocd.Group, Kind: "Application", Namespace: ns, Name: name}
	entry := audit.Entry{Source: argoSource, Action: "propose", Context: sess.Context.Name, Object: ref.String()}
	fail := func(code int, err error) {
		entry.Result, entry.Detail = "denied", truncate(err.Error(), 200)
		s.auditAdd(entry)
		if code == 0 {
			kubeError(w, err)
			return
		}
		httpError(w, code, err.Error())
	}
	var (
		patch   map[string]any
		summary string
		notes   []string
	)
	switch req.Action {
	case "sync":
		cur, err := argocd.Get(r.Context(), sess, ns, name)
		if err != nil {
			fail(0, err)
			return
		}
		if patch, err = argocd.SyncPatch(cur, req.Prune); err != nil {
			fail(http.StatusConflict, err)
			return
		}
		summary = "Sync pelo Argo CD"
		notes = append(notes, "O controlador do Argo CD executa a sincronização ao ver o campo operation; o Leme só grava o pedido.")
		if req.Prune {
			summary += " com prune"
			notes = append(notes, "Prune: o Argo CD apaga do cluster os recursos que não estão mais no Git.")
		}
		app := argocd.ParseApp(cur)
		if app.Automated {
			notes = append(notes, "Esta Application tem sync automático: o controlador já sincroniza sozinho.")
		}
		if !app.Local {
			notes = append(notes, remoteNote(app.Destination))
		}
	case "refresh":
		patch = argocd.RefreshPatch(req.Hard)
		summary = "Refresh pelo Argo CD"
		if req.Hard {
			summary = "Refresh forçado (hard) pelo Argo CD"
			notes = append(notes, "Hard refresh: o Argo CD descarta o cache de manifestos e gera tudo de novo.")
		}
	default:
		fail(http.StatusBadRequest, errors.New("action must be sync or refresh"))
		return
	}
	p, err := s.changes.ProposeWith(r.Context(), sess, ref, patch, summary, argoSource, sess.CheckWritable() == nil, argocd.Allow)
	if err != nil {
		fail(0, err)
		return
	}
	entry.Result = "ok"
	s.auditAdd(entry)
	writeJSON(w, argoProposal{Proposal: p.View(false), Notes: notes})
}

// remoteNote warns that the destination is another cluster: the read-only lock and the production
// confirmation of the connected cluster do not protect it.
func remoteNote(d argocd.Destination) string {
	dest := d.Name
	if dest == "" {
		dest = d.Server
	}
	return "Destino remoto (" + dest + "): o Argo CD aplica em outro cluster, que o Leme não vê. A trava de somente leitura e a confirmação de produção valem só para o cluster conectado."
}

// argoApply applies a proposal made by this feature. Proposals from other sources (Copilot, MCP)
// are not accepted here.
func (s *Server) argoApply(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	tok := r.PathValue("token")
	// Applying is the user's click in the app: audited as a UI change of the Argo CD Application.
	entry := audit.Entry{Source: audit.SourceUI, Action: "argocd-apply", Context: sess.Context.Name}
	deny := func(err error) {
		entry.Result, entry.Detail = "denied", truncate(err.Error(), 200)
		s.auditAdd(entry)
		if errors.Is(err, cluster.ErrReadOnly) {
			httpError(w, http.StatusForbidden, err.Error())
			return
		}
		kubeError(w, err)
	}
	pending, err := s.changes.Get(sess.Context.ID, tok)
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	entry.Object = pending.Ref.String()
	if pending.Source != argoSource || argocd.Allow(pending.Ref.Group, pending.Ref.Kind) != nil {
		deny(errors.New("this proposal was not made by the Argo CD integration"))
		return
	}
	p, err := s.changes.Apply(r.Context(), sess, tok, sess.CheckWritable)
	if err != nil {
		deny(err)
		return
	}
	entry.Result = "ok"
	s.auditAdd(entry)
	s.log.Info("change applied", "context", sess.Context.Name, "object", p.Ref.String(), "source", p.Source)
	writeJSON(w, map[string]any{"applied": true, "ref": p.Ref})
}
