package kube

import (
	"net"
	"net/url"
	"strings"
)

// SameClusterDomain reports whether host is the API server itself or a host under the cluster's own
// domain (e.g. OpenShift routes *.apps.<cluster> for api.<cluster>). Credentials (bearer tokens,
// passwords) are only ever sent to such hosts, so a mistyped or injected URL cannot receive them.
func SameClusterDomain(apiServer, host string) bool {
	a, err := url.Parse(apiServer)
	if err != nil {
		return false
	}
	api := strings.ToLower(a.Hostname())
	host = strings.ToLower(host)
	if host == api {
		return true
	}
	if net.ParseIP(api) != nil {
		return false
	}
	base := strings.TrimPrefix(api, "api.")
	// Require at least "<cluster>.<tld>" so "api.com" cannot authorize every *.com host.
	return base != api && strings.Count(base, ".") >= 1 && strings.HasSuffix(host, "."+base)
}
