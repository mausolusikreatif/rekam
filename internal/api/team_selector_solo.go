//go:build solo

package api

import "net/http"

const teamHeader = "X-Rekam-Team"

// Solo has no shared-workspace selector. Keep the middleware slot in the
// common server chain as a no-op so the core request pipeline stays identical
// without shipping team routing code.
func (s *Server) teamSelector(next http.Handler) http.Handler {
	return next
}
