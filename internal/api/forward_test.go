package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ucok23/rekam/internal/engine"
)

// bufferedRequest builds a request carrying body in the context slot bufferBody
// would have stashed, so respondErr's forwarding path can be tested directly
// without routing a real request through the full middleware chain.
func bufferedRequest(t *testing.T, method, target string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), bufferedBodyKey{}, body))
	req.Header.Set("Authorization", "Bearer whatever")
	return req
}

func notPrimaryErr(owner string) error {
	return &engine.ActionableError{
		Code:    "not_primary",
		Message: "this node is not the write-primary for this workspace right now",
		Context: map[string]string{"owner": owner},
	}
}

func TestRespondErrForwardsNotPrimaryToOwner(t *testing.T) {
	var gotMethod, gotPath, gotBody, gotAuth, gotForwarded string
	owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotAuth = r.Header.Get("Authorization")
		gotForwarded = r.Header.Get(forwardedHeader)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"from-owner"}`))
	}))
	defer owner.Close()

	s := &Server{forwardClient: owner.Client()}
	body := []byte(`{"title":"hello"}`)
	req := bufferedRequest(t, "POST", "/memory", body)
	w := httptest.NewRecorder()

	s.respondErr(w, req, notPrimaryErr(owner.URL))

	if gotMethod != "POST" || gotPath != "/memory" {
		t.Errorf("owner received %s %s, want POST /memory", gotMethod, gotPath)
	}
	if gotBody != string(body) {
		t.Errorf("owner received body %q, want %q", gotBody, string(body))
	}
	if gotAuth != "Bearer whatever" {
		t.Errorf("owner received Authorization %q, want it forwarded through", gotAuth)
	}
	if gotForwarded != "1" {
		t.Errorf("forwarded request should carry %s: 1, got %q", forwardedHeader, gotForwarded)
	}

	if w.Code != http.StatusCreated {
		t.Errorf("client response status = %d, want %d (the owner's status, streamed back)", w.Code, http.StatusCreated)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("client response body not valid JSON: %v (%s)", err, w.Body.String())
	}
	if got["id"] != "from-owner" {
		t.Errorf("client response body = %v, want the owner's body streamed back verbatim", got)
	}
}

func TestRespondErrDoesNotForwardAnAlreadyForwardedRequest(t *testing.T) {
	hit := false
	owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer owner.Close()

	s := &Server{forwardClient: owner.Client()}
	req := bufferedRequest(t, "POST", "/memory", []byte(`{}`))
	req.Header.Set(forwardedHeader, "1") // already forwarded once — must not loop
	w := httptest.NewRecorder()

	s.respondErr(w, req, notPrimaryErr(owner.URL))

	if hit {
		t.Error("an already-forwarded request must not be forwarded again")
	}
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d (plain not_primary error, not a forward)", w.Code, http.StatusConflict)
	}
}

func TestRespondErrFallsBackWhenOwnerIsNotAURL(t *testing.T) {
	s := &Server{forwardClient: http.DefaultClient}
	req := bufferedRequest(t, "POST", "/memory", []byte(`{}`))
	w := httptest.NewRecorder()

	// s3.Leaser's default Owner (no --replicate-owner configured) is a bare
	// "hostname:pid" string — not a URL, so it must never be dialed.
	s.respondErr(w, req, notPrimaryErr("some-host:12345"))

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
	var ae engine.ActionableError
	if err := json.Unmarshal(w.Body.Bytes(), &ae); err != nil {
		t.Fatalf("response not valid ActionableError JSON: %v", err)
	}
	if ae.Code != "not_primary" {
		t.Errorf("code = %q, want not_primary", ae.Code)
	}
}

func TestRespondErrFallsBackWhenOwnerUnreachable(t *testing.T) {
	s := &Server{forwardClient: &http.Client{}}
	req := bufferedRequest(t, "POST", "/memory", []byte(`{}`))
	w := httptest.NewRecorder()

	// Port 1 is reserved/unassigned — guaranteed nothing is listening.
	s.respondErr(w, req, notPrimaryErr("http://127.0.0.1:1"))

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d (fall back to the plain error, not hang or 502)", w.Code, http.StatusConflict)
	}
}

func TestRespondErrDoesNotForwardOtherErrorCodes(t *testing.T) {
	hit := false
	owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer owner.Close()

	s := &Server{forwardClient: owner.Client()}
	req := bufferedRequest(t, "POST", "/memory", []byte(`{}`))
	w := httptest.NewRecorder()

	s.respondErr(w, req, &engine.ActionableError{
		Code:    "write_not_allowed",
		Message: "read-only",
		Context: map[string]string{"owner": owner.URL}, // present but irrelevant to this code
	})

	if hit {
		t.Error("only not_primary should ever trigger a forward")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}
