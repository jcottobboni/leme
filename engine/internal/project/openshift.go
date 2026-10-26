package project

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// OpenShift and related kinds. Registered separately so the core table stays readable.
func init() {
	projectors["Route"] = projector{cols(Column{Key: "url", Label: "URL", Width: 320}, Column{Key: "service", Label: "Service", Width: 160}, Column{Key: "tls", Label: "TLS", Width: 110}, Column{Key: "status", Label: "Status", Type: "status", Width: 100}), route}
	projectors["DeploymentConfig"] = projector{cols(Column{Key: "ready", Label: "Ready", Width: 80}, Column{Key: "revision", Label: "Revision", Type: "number", Width: 80}, Column{Key: "status", Label: "Status", Type: "status", Width: 110}), deploymentConfig}
	projectors["ReplicationController"] = projector{cols(Column{Key: "desired", Label: "Desired", Type: "number", Width: 80}, Column{Key: "ready", Label: "Ready", Width: 80}, Column{Key: "status", Label: "Status", Type: "status", Width: 110}), workload}
	projectors["BuildConfig"] = projector{cols(Column{Key: "strategy", Label: "Strategy", Width: 100}, Column{Key: "source", Label: "Source", Width: 300}, Column{Key: "lastVersion", Label: "Last build", Type: "number", Width: 90}), buildConfig}
	projectors["Build"] = projector{cols(Column{Key: "status", Label: "Status", Type: "status", Width: 110}, Column{Key: "strategy", Label: "Strategy", Width: 100}, Column{Key: "duration", Label: "Duration", Width: 90}, Column{Key: "config", Label: "BuildConfig", Width: 180}), build}
	projectors["ImageStream"] = projector{cols(Column{Key: "tags", Label: "Tags", Width: 200}, Column{Key: "repository", Label: "Repository", Width: 360}), imageStream}
	projectors["Project"] = projector{[]Column{{Key: "name", Label: "Name", Width: 300}, {Key: "displayName", Label: "Display name", Width: 220}, {Key: "status", Label: "Status", Type: "status", Width: 110}, ageCol}, openshiftProject}
}

var routeHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)
var routePath = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@%/-]*$`)

// RouteURL returns the public URL of an OpenShift Route, or "" if the host or path is not plausible
// (the value comes from the cluster and becomes a clickable link).
func RouteURL(u *unstructured.Unstructured) string {
	if !strings.HasPrefix(u.GetAPIVersion(), "route.openshift.io/") {
		return ""
	}
	host := str(u, "spec", "host")
	if host == "" {
		for _, in := range slice(u, "status", "ingress") {
			if m, ok := in.(map[string]any); ok {
				host = strOr(m["host"], "")
				break
			}
		}
	}
	if host == "" || !routeHost.MatchString(host) {
		return ""
	}
	path := str(u, "spec", "path")
	if path != "" && !routePath.MatchString(path) {
		path = ""
	}
	scheme := "http"
	if str(u, "spec", "tls", "termination") != "" {
		scheme = "https"
	}
	return scheme + "://" + host + path
}

func route(u *unstructured.Unstructured, r *Row) {
	if !strings.HasPrefix(u.GetAPIVersion(), "route.openshift.io/") {
		// Another API's "Route" kind (Knative, Contour…): generic columns.
		genericConditions(u, r)
		return
	}
	r.Cells["url"] = RouteURL(u)
	svc := str(u, "spec", "to", "name")
	if p := fmt.Sprint(nested(u, "spec", "port", "targetPort")); p != "" && p != "<nil>" {
		svc += ":" + p
	}
	r.Cells["service"] = svc
	tls := str(u, "spec", "tls", "termination")
	if tls == "" {
		tls = "—"
	}
	r.Cells["tls"] = tls
	r.Cells["status"], r.Health = "Pending", "warn"
	for _, in := range slice(u, "status", "ingress") {
		m, _ := in.(map[string]any)
		conds, _ := m["conditions"].([]any)
		for _, c := range conds {
			cm, _ := c.(map[string]any)
			if cm["type"] == "Admitted" {
				if cm["status"] == "True" {
					r.Cells["status"], r.Health = "Admitted", "ok"
				} else {
					r.Cells["status"], r.Health = "Rejected", "error"
				}
			}
		}
	}
}

func nested(u *unstructured.Unstructured, path ...string) any {
	v, _, _ := unstructured.NestedFieldNoCopy(u.Object, path...)
	return v
}

func deploymentConfig(u *unstructured.Unstructured, r *Row) {
	workload(u, r)
	r.Cells["revision"] = num(u, "status", "latestVersion")
	for _, c := range slice(u, "status", "conditions") {
		cm, _ := c.(map[string]any)
		if cm["type"] == "Progressing" && cm["status"] == "False" {
			r.Cells["status"], r.Health = "Failed", "error"
		}
	}
}

func buildConfig(u *unstructured.Unstructured, r *Row) {
	r.Cells["strategy"] = str(u, "spec", "strategy", "type")
	src := str(u, "spec", "source", "git", "uri")
	if src == "" {
		src = str(u, "spec", "source", "type")
	}
	r.Cells["source"] = src
	r.Cells["lastVersion"] = num(u, "status", "lastVersion")
	r.Health = "ok"
}

func build(u *unstructured.Unstructured, r *Row) {
	phase := str(u, "status", "phase")
	if phase == "" {
		phase = "New"
	}
	r.Cells["status"] = phase
	r.Cells["strategy"] = str(u, "spec", "strategy", "type")
	r.Cells["config"] = u.GetLabels()["openshift.io/build-config.name"]
	switch phase {
	case "Complete":
		r.Health = "ok"
	case "Failed", "Error":
		r.Health = "error"
	case "Cancelled":
		r.Health = "neutral"
	default:
		r.Health = "warn"
	}
	start, err1 := time.Parse(time.RFC3339, str(u, "status", "startTimestamp"))
	end, err2 := time.Parse(time.RFC3339, str(u, "status", "completionTimestamp"))
	if err1 == nil {
		if err2 != nil {
			end = time.Now()
		}
		r.Cells["duration"] = end.Sub(start).Round(time.Second).String()
	}
}

func imageStream(u *unstructured.Unstructured, r *Row) {
	var tags []string
	for _, t := range slice(u, "status", "tags") {
		if m, ok := t.(map[string]any); ok {
			tags = append(tags, strOr(m["tag"], ""))
		}
	}
	if len(tags) > 6 {
		tags = append(tags[:6], fmt.Sprintf("+%d", len(tags)-6))
	}
	r.Cells["tags"] = strings.Join(tags, ", ")
	repo := str(u, "status", "publicDockerImageRepository")
	if repo == "" {
		repo = str(u, "status", "dockerImageRepository")
	}
	r.Cells["repository"] = repo
	r.Health = "ok"
}

func openshiftProject(u *unstructured.Unstructured, r *Row) {
	r.Cells["displayName"] = u.GetAnnotations()["openshift.io/display-name"]
	p := str(u, "status", "phase")
	if p == "" {
		p = "Active"
	}
	r.Cells["status"] = p
	r.Health = map[bool]string{true: "ok", false: "warn"}[p == "Active"]
}
