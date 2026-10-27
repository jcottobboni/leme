package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/jcottobboni/leme/engine/internal/audit"
	"github.com/jcottobboni/leme/engine/internal/change"
	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/flux"
)

// fluxRoutes serves the Flux CD panel and its reconcile, suspend and resume proposals (M7, RF04).
// Everything goes through the Kubernetes API with the user's kubeconfig: no flux CLI.
func (s *Server) fluxRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/clusters/{id}/flux", s.withSession(s.fluxList))
	m.HandleFunc("POST /api/clusters/{id}/flux/{kind}/{ns}/{name}/propose", s.withSession(s.fluxPropose))
	m.HandleFunc("POST /api/clusters/{id}/flux/apply/{token}", s.withSession(s.fluxApply))
}

// fluxSource tags proposals and audit entries of this feature.
const fluxSource = "flux"

// fluxNow is the clock of the reconcile annotation (tests replace it).
var fluxNow = time.Now

func (s *Server) fluxList(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	ov, err := flux.List(r.Context(), sess, r.URL.Query().Get("namespace"))
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, ov)
}

type fluxProposeRequest struct {
	Action string `json:"action"` // reconcile | suspend | resume
}

type fluxProposal struct {
	change.Proposal
	Notes []string `json:"notes,omitempty"`
}

func (s *Server) fluxPropose(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req fluxProposeRequest
	if !decode(w, r, &req) {
		return
	}
	kind, ns, name := r.PathValue("kind"), r.PathValue("ns"), r.PathValue("name")
	group, ok := flux.GroupOf(kind)
	if !ok {
		httpError(w, http.StatusBadRequest, flux.ErrUnknownKind.Error())
		return
	}
	if _, ok := sess.LookupKind(group, kind); !ok {
		httpError(w, http.StatusNotFound, flux.Hint)
		return
	}
	ref := change.Ref{Group: group, Kind: kind, Namespace: ns, Name: name}
	entry := audit.Entry{Source: fluxSource, Action: "propose", Context: sess.Context.Name, Object: ref.String(), Detail: req.Action}
	fail := func(code int, err error) {
		entry.Result, entry.Detail = "denied", truncate(req.Action+": "+err.Error(), 200)
		s.auditAdd(entry)
		if code == 0 {
			kubeError(w, err)
			return
		}
		httpError(w, code, err.Error())
	}
	cur, err := flux.Get(r.Context(), sess, kind, ns, name)
	if err != nil {
		fail(0, err)
		return
	}
	var (
		patch   map[string]any
		summary string
		notes   []string
	)
	switch req.Action {
	case "reconcile":
		patch, err = flux.ReconcilePatch(cur, fluxNow())
		summary = "Reconcile pelo Flux"
		notes = append(notes, "O controlador do Flux reconcilia ao ver a anotação "+flux.ReconcileAnnotation+"; o Leme só grava o pedido.")
	case "suspend":
		patch, err = flux.SuspendPatch(cur)
		summary = "Suspender no Flux"
		notes = append(notes, "Suspenso, o controlador para de reconciliar este objeto (inclusive correções de drift) até ser retomado.")
	case "resume":
		patch, err = flux.ResumePatch(cur, fluxNow())
		summary = "Retomar no Flux"
		notes = append(notes, "Retomar remove spec.suspend e pede um reconcile, como o flux resume.")
	default:
		fail(http.StatusBadRequest, errors.New("action must be reconcile, suspend or resume"))
		return
	}
	if err != nil {
		fail(http.StatusConflict, err)
		return
	}
	if kind == "Kustomization" || kind == "HelmRelease" {
		notes = append(notes, "O que o controlador aplicar a partir daí não passa pela prévia do Leme: vem do Git, do repositório OCI ou do chart.")
	}
	p, err := s.changes.ProposeWith(r.Context(), sess, ref, patch, summary, fluxSource, sess.CheckWritable() == nil, flux.Allow)
	if err != nil {
		fail(0, err)
		return
	}
	entry.Result = "ok"
	s.auditAdd(entry)
	writeJSON(w, fluxProposal{Proposal: p.View(false), Notes: notes})
}

// fluxApply applies a proposal made by this feature; proposals from other sources are refused.
func (s *Server) fluxApply(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	tok := r.PathValue("token")
	entry := audit.Entry{Source: fluxSource, Action: "apply", Context: sess.Context.Name}
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
	entry.Detail = pending.Summary
	if pending.Source != fluxSource || flux.Allow(pending.Ref.Group, pending.Ref.Kind) != nil {
		deny(errors.New("this proposal was not made by the Flux integration"))
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
