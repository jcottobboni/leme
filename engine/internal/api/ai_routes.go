package api

import "net/http"

// aiState holds the Copilot's in-memory state (API keys handed over by the desktop shell).
type aiState struct{}

func newAIState() *aiState { return &aiState{} }

// aiRoutes serves the Copilot (M4, group G3).
func (s *Server) aiRoutes(m *http.ServeMux) {}
