package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"

	"github.com/Ucok23/rekam/internal/db"
	"github.com/Ucok23/rekam/internal/engine"
)

// Scenarios: spec/files.md (FILE-01..07)

// uploadImage posts a one-pixel-ish blob as a multipart image and returns the
// recorder.
func uploadImage(t *testing.T, srv *Server, apiKey, contentType string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="pic"`)
	h.Set("Content-Type", contentType)
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	mw.Close()

	req := httptest.NewRequest("POST", "/files", &buf)
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

func TestFileUploadAndFetchRoundTrip(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	payload := []byte("\x89PNG\r\n\x1a\n-not-a-real-png-but-bytes")
	rr := uploadImage(t, srv, key, "image/png", payload)
	// FILE-01
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload: want 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct{ ID, URL string }
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.URL == "" || !strings.HasPrefix(out.URL, "/files/") {
		t.Fatalf("bad url: %q", out.URL)
	}

	// Fetch via the capability URL with NO Authorization header.
	req := httptest.NewRequest("GET", out.URL, nil)
	got := httptest.NewRecorder()
	srv.Handler().ServeHTTP(got, req)
	// FILE-02
	if got.Code != http.StatusOK {
		t.Fatalf("fetch: want 200, got %d: %s", got.Code, got.Body.String())
	}
	if ct := got.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type: want image/png, got %q", ct)
	}
	if !bytes.Equal(got.Body.Bytes(), payload) {
		t.Errorf("body mismatch: got %d bytes, want %d", got.Body.Len(), len(payload))
	}
}

func TestFileUploadRejectsNonImage(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	rr := uploadImage(t, srv, key, "application/pdf", []byte("%PDF-1.4"))
	// FILE-03
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("want 415 for pdf, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestFileFetchMissingReturns404(t *testing.T) {
	srv, _, cleanup := testServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/files/no-such-identity/no-such-file", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	// FILE-04
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// fileTestServerWithReadOnly is testServer plus a second identity created with
// allowWrite=false, for exercising FILE-07's write-permission gate.
func fileTestServerWithReadOnly(t *testing.T) (srv *Server, writeKey, readOnlyKey string, cleanup func()) {
	t.Helper()

	regF, err := os.CreateTemp("", "api-reg-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	regF.Close()
	memF, err := os.CreateTemp("", "api-mem-*.memory")
	if err != nil {
		t.Fatal(err)
	}
	memF.Close()
	roF, err := os.CreateTemp("", "api-mem-ro-*.memory")
	if err != nil {
		t.Fatal(err)
	}
	roF.Close()

	reg, err := db.OpenRegistry(regF.Name())
	if err != nil {
		t.Fatal(err)
	}

	writeKey = "api-test-key"
	if _, err := reg.Create("alice", writeKey, memF.Name(), true); err != nil {
		t.Fatal(err)
	}
	readOnlyKey = "api-test-readonly-key"
	if _, err := reg.Create("bob", readOnlyKey, roF.Name(), false); err != nil {
		t.Fatal(err)
	}

	eng := engine.New(reg, 6000)
	srv = NewServer(eng, ":0", "", "")

	cleanup = func() {
		reg.Close()
		os.Remove(regF.Name())
		os.Remove(memF.Name())
		os.Remove(roF.Name())
	}
	return
}

func TestFileUploadRequiresFileField(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.Close() // no "file" part at all

	req := httptest.NewRequest("POST", "/files", &buf)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	// FILE-05
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing file field, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestFileUploadRejectsOversized(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	oversized := bytes.Repeat([]byte("a"), maxUploadBytes+1)
	rr := uploadImage(t, srv, key, "image/png", oversized)
	// FILE-06
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413 for oversized upload, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestFileUploadRequiresWritePermission(t *testing.T) {
	srv, _, readOnlyKey, cleanup := fileTestServerWithReadOnly(t)
	defer cleanup()

	rr := uploadImage(t, srv, readOnlyKey, "image/png", []byte("\x89PNG\r\n\x1a\n"))
	// FILE-07
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 for read-only identity, got %d: %s", rr.Code, rr.Body.String())
	}
}
