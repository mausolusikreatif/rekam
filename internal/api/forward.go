package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/Ucok23/rekam/internal/engine"
)

// forwardedHeader marks a request this node has already forwarded once, so a
// receiving node whose own lease-holder view is stale can't bounce it back
// and loop forever. A node seeing this header on a request it can't serve as
// primary fails closed with the ordinary not_primary error instead of
// forwarding again.
const forwardedHeader = "X-Rekam-Forwarded"

type bufferedBodyKey struct{}

// bufferBody reads the entire request body into memory upfront and replaces
// r.Body with a fresh reader over it, so a handler downstream can still
// consume it normally (json.Decode, etc.) while the raw bytes stay available
// afterward — specifically for respondErr to replay verbatim onto another
// node if the request turns out to hit a not_primary lease miss (see
// docs/plan-distributed-tenants.md §5). Cheap for ordinary JSON bodies; a
// known cost for large file uploads (POST /files) — see forwardToOwner's doc
// comment.
func bufferBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			body, err := io.ReadAll(r.Body)
			r.Body.Close()
			if err != nil {
				respond(w, http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r = r.WithContext(context.WithValue(r.Context(), bufferedBodyKey{}, body))
		}
		next.ServeHTTP(w, r)
	})
}

// respondErr writes err as the appropriate HTTP response — or, if err is a
// not_primary ActionableError carrying a reachable owner URL and this request
// hasn't already been forwarded once, replays the request onto that owner
// instead and streams its response back, so a client talking to any node gets
// a normal response rather than having to retry elsewhere itself.
func (s *Server) respondErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae *engine.ActionableError
	if errors.As(err, &ae) && ae.Code == "not_primary" && r.Header.Get(forwardedHeader) == "" {
		if owner := ownerFromContext(ae); owner != "" {
			if s.forwardToOwner(w, r, owner) {
				return
			}
			// Forwarding attempt failed (owner unreachable, bad URL, etc.) —
			// fall through to the ordinary error response below rather than
			// leaving the client hanging.
		}
	}
	writeActionableError(w, err)
}

// ownerFromContext pulls the forwarding-target URL out of a not_primary
// error's Context, added by engine.resolveTenant. Empty if absent or not a
// string (e.g. the hostname:pid default s3.Leaser falls back to when no
// --replicate-owner was configured — not a URL, so not forwardable).
func ownerFromContext(ae *engine.ActionableError) string {
	m, ok := ae.Context.(map[string]string)
	if !ok {
		return ""
	}
	owner := m["owner"]
	if owner == "" {
		return ""
	}
	if u, err := url.Parse(owner); err != nil || u.Scheme == "" || u.Host == "" {
		return "" // not a URL we can forward to (e.g. the hostname:pid default)
	}
	return owner
}

// forwardToOwner replays r onto owner + r.URL's path/query, using the body
// bufferBody stashed, and streams the response back onto w verbatim. Returns
// false (having written nothing) if forwarding couldn't be attempted or the
// owner couldn't be reached at all, so the caller falls back to the ordinary
// error response instead of leaving the client without any response.
//
// Known limitation: this forwards the fully-buffered request body, which for
// POST /files (blob uploads) means an upload is held in memory twice
// (once by bufferBody, once by http.NewRequest's reader) before forwarding.
// This memory cost is strictly bounded because POST /files enforces a 2 MiB limit
// (maxUploadBytes in files_handlers.go). Streaming forward without body buffering
// remains on the roadmap for future larger attachment sizes.
func (s *Server) forwardToOwner(w http.ResponseWriter, r *http.Request, owner string) bool {
	body, _ := r.Context().Value(bufferedBodyKey{}).([]byte)

	target := owner + r.URL.RequestURI()
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		log.Printf("forward to %s: build request: %v", owner, err)
		return false
	}
	req.Header = r.Header.Clone()
	req.Header.Set(forwardedHeader, "1")
	if len(body) > 0 {
		req.ContentLength = int64(len(body))
	}

	client := s.forwardClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("forward to %s: %v", owner, err)
		return false
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	return true
}

// writeActionableError is respondErr's fallback: the direct, non-forwarded
// error response — the entire body of what respondErr used to be before
// write-forwarding existed.
func writeActionableError(w http.ResponseWriter, err error) {
	var ae *engine.ActionableError
	if errors.As(err, &ae) {
		var status int
		switch ae.Code {
		case "auth_failed":
			status = http.StatusUnauthorized
		case "write_not_allowed", "taxonomy_forbidden", "forbidden", "not_a_member":
			status = http.StatusForbidden
		case "not_found":
			status = http.StatusNotFound
		case "version_conflict", "last_owner", "title_conflict", "has_inbound_links":
			status = http.StatusConflict
		case "deleted":
			// 410, not 404: the address is known and permanently reserved.
			status = http.StatusGone
		case "not_primary":
			// 409: the request was valid, but this node can't serve it right
			// now — distinct from 400/403/404, and worth a client retrying
			// elsewhere rather than treating it as a permanent rejection.
			status = http.StatusConflict
		default:
			status = http.StatusBadRequest
		}
		respond(w, status, ae)
		return
	}
	respond(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

// defaultForwardClient is used when no *http.Client override is configured on
// Server — a bounded timeout so a forward to a node that's actually down
// fails fast rather than hanging the original client's request.
func defaultForwardClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}
