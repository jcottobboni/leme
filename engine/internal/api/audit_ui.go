package api

import (
	"errors"

	"sigs.k8s.io/yaml"

	"github.com/jcottobboni/leme/engine/internal/audit"
	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// auditAdd records an entry; a failure to write the audit log is logged, never dropped silently.
func (s *Server) auditAdd(e audit.Entry) {
	if err := s.audit.Add(e); err != nil && s.log != nil {
		s.log.Error("audit write failed", "source", e.Source, "action", e.Action, "object", e.Object, "err", err)
	}
}

// auditUI records a change the user made in the app. err is the outcome of the action (nil = ok).
func (s *Server) auditUI(sess *cluster.Session, action, object, detail string, err error) {
	e := audit.Entry{Source: audit.SourceUI, Action: action, Context: sess.Context.Name, Object: object, Result: "ok", Detail: detail}
	if err != nil {
		e.Result = "error"
		if errors.Is(err, cluster.ErrReadOnly) {
			e.Result = "denied"
		}
		e.Detail = truncate(err.Error(), 200)
	}
	s.auditAdd(e)
}

// objectName is the audit "Kind/namespace/name" of a group/resource reference.
func objectName(sess *cluster.Session, group, resource, namespace, name string) string {
	kind := resource
	if r, ok := sess.Lookup(group, resource); ok {
		kind = r.Kind
	}
	return kind + "/" + namespace + "/" + name
}

// yamlObjectName is the audit name of the object a YAML document describes.
func yamlObjectName(doc, defaultNS string) string {
	var m struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	if yaml.Unmarshal([]byte(doc), &m) != nil {
		return ""
	}
	if m.Metadata.Namespace == "" {
		m.Metadata.Namespace = defaultNS
	}
	return m.Kind + "/" + m.Metadata.Namespace + "/" + m.Metadata.Name
}

func helmRelease(namespace, name string) string { return "HelmRelease/" + namespace + "/" + name }
