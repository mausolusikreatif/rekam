//go:build solo

package api

import "net/http"

// Edition names what this binary can do, for the startup banner and /me.
const Edition = "solo"

// registerEditionRoutes adds nothing beyond the core in a solo build: no
// shared corpora, no user management, no signup, no plans. Those handlers
// are not merely unrouted here — registerTeamRoutes and
// registerManagedRoutes do not exist in this build at all.
func (s *Server) registerEditionRoutes(mux *http.ServeMux) {}
