package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/Ucok23/rekam/internal/db"
	"github.com/Ucok23/rekam/internal/engine"
)

// adminUserView is the admin-facing projection of an identity (includes email,
// admin flag, and memory path — fields the normal identityView omits).
// storage_bytes is stat'd directly off memory_path (WAL/SHM sidecars
// included, see db.FileSize) rather than through an opened MemoryDB handle —
// listing every identity shouldn't have to open every tenant file just to
// size it. A stat failure (rare — permissions, a path since moved) reports 0
// rather than failing the whole listing.
func adminUserView(id *db.Identity) map[string]any {
	size, _ := db.FileSize(id.MemoryPath)
	return map[string]any{
		"id":            id.ID,
		"name":          id.Name,
		"email":         id.Email,
		"allow_write":   id.AllowWrite,
		"is_admin":      id.IsAdmin,
		"memory_path":   id.MemoryPath,
		"storage_bytes": size,
		"created_at":    id.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// handleDashboard serves GET /admin/dashboard — retained for backward-compat with bookmarks.
// It returns an unauthenticated 302 redirect to /ui/#/admin and discloses no sensitive data itself;
// authentication and authorization are strictly enforced by the SPA console and all underlying /admin/* API endpoints.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/ui/#/admin", http.StatusFound)
}

// handleAdminStats serves GET /admin/stats — aggregate registry counts.
func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "admin access required"})
		return
	}
	stats, err := s.eng.RegistryStats()
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	respond(w, http.StatusOK, stats)
}

// handleListUsers serves GET /admin/users — every identity in the registry.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "admin access required"})
		return
	}
	ids, err := s.eng.AllIdentities()
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	users := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		users = append(users, adminUserView(id))
	}
	respond(w, http.StatusOK, map[string]any{"users": users})
}

// handleCreateUser serves POST /admin/users — admin-provisioned account creation.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "admin access required"})
		return
	}
	if s.usersDir == "" {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": "no users directory configured"})
		return
	}
	var body struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		Name       string `json:"name"`
		AllowWrite *bool  `json:"allow_write"`
		IsAdmin    bool   `json:"is_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	allowWrite := true
	if body.AllowWrite != nil {
		allowWrite = *body.AllowWrite
	}
	suffix, err := randomHex(12)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	memoryPath := filepath.Join(s.usersDir, "user-"+suffix+".rekam")
	identity, err := s.eng.CreateUser(body.Name, body.Email, body.Password, memoryPath, allowWrite, body.IsAdmin)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"user": adminUserView(identity)})
}

// handleUpdateUser serves PATCH /admin/users/{id} — toggle write/admin flags and
// optionally reset the password.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "admin access required"})
		return
	}
	id := r.PathValue("id")
	var body struct {
		AllowWrite *bool   `json:"allow_write"`
		IsAdmin    *bool   `json:"is_admin"`
		Password   *string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if err := s.eng.UpdateUserFlags(id, body.AllowWrite, body.IsAdmin); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.Password != nil {
		if err := s.eng.SetUserPassword(id, *body.Password); err != nil {
			respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	identity, err := s.eng.IdentityByID(id)
	if err != nil {
		respond(w, http.StatusOK, map[string]any{"updated": id})
		return
	}
	respond(w, http.StatusOK, map[string]any{"user": adminUserView(identity)})
}

// adminTeamView is the admin-facing projection of a team — db.Team/
// engine.AdminTeam carry no json tags (nothing has serialized them directly
// before this; every existing caller, e.g. handleRenameTeam, hand-picks
// fields into its own map), so this is the one place that decides the wire
// shape, matching adminUserView's role for identities.
func adminTeamView(t *engine.AdminTeam) map[string]any {
	size, _ := db.FileSize(t.Path)
	return map[string]any{
		"id":               t.ID,
		"name":             t.Name,
		"plan":             t.Plan,
		"seat_limit":       t.SeatLimit,
		"member_count":     t.MemberCount,
		"created_by":       t.CreatedBy,
		"created_by_name":  t.CreatedByName,
		"created_by_email": t.CreatedByEmail,
		"storage_bytes":    size,
		"created_at":       t.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// handleListTeams serves GET /admin/teams — every team in the registry, for
// the admin console's team directory.
func (s *Server) handleListTeams(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "admin access required"})
		return
	}
	teams, err := s.eng.AllTeams()
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	views := make([]map[string]any, 0, len(teams))
	for _, t := range teams {
		views = append(views, adminTeamView(t))
	}
	respond(w, http.StatusOK, map[string]any{"teams": views})
}

// handleAdminDeleteTeam serves DELETE /admin/teams/{id} — soft-deletes any
// team, admin-only. Unlike DELETE /team (a member's own Owner-only action via
// the team selector), this needs no acting-in-it context: an admin names the
// team directly.
func (s *Server) handleAdminDeleteTeam(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "admin access required"})
		return
	}
	id := r.PathValue("id")
	if err := s.eng.AdminDeleteTeam(id); err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]string{"deleted": id})
}

// handleRevokeIdentity serves DELETE /admin/users/{id} and DELETE /identities/{id}
// — removes an identity. Permitted for an admin, or for a caller revoking their
// own identity (same-identity scope). Any other caller is forbidden.
func (s *Server) handleRevokeIdentity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireAdmin(r) {
		caller, err := s.eng.GetCurrentIdentity(apiKeyFromRequest(r))
		if err != nil {
			respond(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		if caller.ID != id {
			respond(w, http.StatusForbidden, map[string]string{"error": "only an admin or the identity owner may revoke this key"})
			return
		}
	}
	if err := s.eng.DeleteIdentity(id); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	respond(w, http.StatusOK, map[string]string{"revoked": id})
}
