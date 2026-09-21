package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Ucok23/rekam/internal/db"
	"github.com/Ucok23/rekam/internal/engine"
)

func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// respondErr (the error-response entry point every handler calls) and its
// write-forwarding logic live in forward.go.

func apiKeyFromRequest(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && h[:7] == "Bearer " {
		return h[7:]
	}
	return ""
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	// The web UI builds the full taxonomy graph from leaf paths, so request every
	// level (a large depth disables rollup); MCP agents drill in with prefix/depth instead.
	entries, err := s.eng.Catalog(key, "", 1000)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"taxonomy": entries})
}

// handleTaxonomyTemplate serves GET /taxonomy/template — the corpus's effective
// taxonomy scaffold (saved custom, or the shipped default when none is saved).
func (s *Server) handleTaxonomyTemplate(w http.ResponseWriter, r *http.Request) {
	res, err := s.eng.TaxonomyTemplate(apiKeyFromRequest(r))
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, res)
}

// handleSetTaxonomyTemplate serves PUT /taxonomy/template — replace the corpus's
// template (Owner/Admin only). An empty branches list reverts to the default.
func (s *Server) handleSetTaxonomyTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Branches []db.TemplateBranch `json:"branches"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	res, err := s.eng.SetTaxonomyTemplate(apiKeyFromRequest(r), body.Branches)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, res)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	q := r.URL.Query().Get("q")
	taxonomy := r.URL.Query().Get("taxonomy")
	tokenBudget := 0
	if tb := r.URL.Query().Get("token_budget"); tb != "" {
		n, err := strconv.Atoi(tb)
		if err != nil || n < 0 {
			respond(w, http.StatusBadRequest, map[string]string{"error": "token_budget must be a non-negative integer"})
			return
		}
		tokenBudget = n
	}

	result, err := s.eng.Search(key, q, taxonomy, tokenBudget)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (s *Server) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	id := r.PathValue("id")
	mem, err := s.eng.GetMemory(key, id)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, mem)
}

func (s *Server) handleWriteMemory(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	var input engine.WriteInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	mem, err := s.eng.WriteMemory(key, input)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusCreated, mem)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	identity, err := s.eng.GetCurrentIdentity(key)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{
		"id":          identity.ID,
		"name":        identity.Name,
		"allow_write": identity.AllowWrite,
		"created_at":  identity.CreatedAt.Format(time.RFC3339),
		// EDTN-05: which build this is. A client pointed at a server it did
		// not choose — the native app, above all — can size its UI to what
		// exists here instead of probing for 404s.
		"edition": Edition,
	})
}

func (s *Server) handleListIdentities(w http.ResponseWriter, r *http.Request) {
	// Enumerating every user is an admin-only capability. A plain valid key
	// (e.g. a minted OAuth grant) must not be able to list the tenant roster.
	if !s.requireAdmin(r) {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "admin key required"})
		return
	}
	ids, err := s.eng.AllIdentities()
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	type safeID struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		AllowWrite bool   `json:"allow_write"`
		CreatedAt  string `json:"created_at"`
	}
	result := make([]safeID, len(ids))
	for i, id := range ids {
		result[i] = safeID{
			ID:         id.ID,
			Name:       id.Name,
			AllowWrite: id.AllowWrite,
			CreatedAt:  id.CreatedAt.Format(time.RFC3339),
		}
	}
	respond(w, http.StatusOK, map[string]any{"identities": result})
}

func (s *Server) handleScope(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	taxonomy := r.URL.Query().Get("taxonomy")
	tokenBudget := 0
	if tb := r.URL.Query().Get("token_budget"); tb != "" {
		n, err := strconv.Atoi(tb)
		if err != nil || n < 0 {
			respond(w, http.StatusBadRequest, map[string]string{"error": "token_budget must be a non-negative integer"})
			return
		}
		tokenBudget = n
	}
	result, err := s.eng.ScopeMemories(key, taxonomy, tokenBudget)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (s *Server) handleRebuildFTS(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	if err := s.eng.RebuildFTS(key); err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleEdgeHealth(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	health, err := s.eng.EdgeHealth(key)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, health)
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	center := r.URL.Query().Get("center")
	taxonomy := r.URL.Query().Get("taxonomy")
	depth := 0
	if d := r.URL.Query().Get("depth"); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			respond(w, http.StatusBadRequest, map[string]string{"error": "depth must be a non-negative integer"})
			return
		}
		depth = n
	}
	g, err := s.eng.Graph(key, center, depth, taxonomy)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, g)
}

func (s *Server) handleSuggestLinks(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 0 {
			respond(w, http.StatusBadRequest, map[string]string{"error": "limit must be a non-negative integer"})
			return
		}
		limit = n
	}
	suggestions, err := s.eng.SuggestLinks(key, limit)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"suggestions": suggestions})
}

func (s *Server) handleReviewDue(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 0 {
			respond(w, http.StatusBadRequest, map[string]string{"error": "limit must be a non-negative integer"})
			return
		}
		limit = n
	}
	cards, stats, err := s.eng.DueReviews(key, limit)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"cards": cards, "stats": stats})
}

func (s *Server) handleAddReview(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	st, err := s.eng.AddToReview(key, r.PathValue("id"))
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, st)
}

func (s *Server) handleRemoveReview(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	if err := s.eng.RemoveFromReview(key, r.PathValue("id")); err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]string{"removed": r.PathValue("id")})
}

func (s *Server) handleGradeReview(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	var body struct {
		Grade int `json:"grade"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	st, err := s.eng.GradeReview(key, r.PathValue("id"), body.Grade)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, st)
}

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	taxonomy := r.URL.Query().Get("taxonomy")

	limit, offset := 0, 0
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 0 {
			respond(w, http.StatusBadRequest, map[string]string{"error": "limit must be a non-negative integer"})
			return
		}
		limit = n
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		n, err := strconv.Atoi(o)
		if err != nil || n < 0 {
			respond(w, http.StatusBadRequest, map[string]string{"error": "offset must be a non-negative integer"})
			return
		}
		offset = n
	}

	result, err := s.eng.ListMemories(key, taxonomy, limit, offset)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"memories": result.Memories, "total": result.Total})
}

func (s *Server) handleUpdateMemory(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	id := r.PathValue("id")
	var input engine.UpdateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	mem, err := s.eng.UpdateMemory(key, id, input)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, mem)
}

func (s *Server) handleDeletedMemories(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tombstones, err := s.eng.DeletedMemories(key, r.URL.Query().Get("taxonomy"), limit)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"deleted": tombstones})
}

func (s *Server) handleMemoryHistory(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	id := r.PathValue("id")
	revs, err := s.eng.MemoryHistory(key, id)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"revisions": revs})
}

func (s *Server) handleRestoreRevision(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	id := r.PathValue("id")
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "version must be an integer"})
		return
	}
	mem, err := s.eng.RestoreRevision(key, id, version)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, mem)
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	id := r.PathValue("id")
	force := r.URL.Query().Get("force") == "true"
	if err := s.eng.DeleteMemory(key, id, force); err != nil {
		s.respondErr(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]string{"deleted": id})
}
