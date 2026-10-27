package api

import "net/http"

// grafanaRoutes detects Grafana/Prometheus services and opens them through a port-forward (M5, feature grafana).
func (s *Server) grafanaRoutes(m *http.ServeMux) {}
