package api

import "net/http"

// trafficRoutes serves observed traffic edges (M5, feature traffic): GET /api/clusters/{id}/traffic.
func (s *Server) trafficRoutes(m *http.ServeMux) {}
