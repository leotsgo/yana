// The graph endpoint: the link structure of every note the caller may
// read, for the graph view.
package server

import "net/http"

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	allowed, ok := s.readableSpaces(w, r)
	if !ok {
		return
	}
	g, err := s.DB.Graph(r.Context(), allowed)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}
