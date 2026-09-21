package updatecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	m := Manifest{
		"solo": {Latest: "0.4.0", MinSupported: "0.2.0", Security: true, Notes: "see advisory"},
		"team": {Latest: "0.4.0"},
	}
	cases := []struct {
		name                             string
		edition, current                 string
		available, security, unsupported bool
	}{
		{"behind and it is a security release", "solo", "0.3.0", true, true, false},
		{"current", "solo", "0.4.0", false, false, false},
		{"ahead of the manifest", "solo", "0.5.0", false, false, false},
		{"below the supported floor", "solo", "0.1.0", true, true, true},
		{"behind, no security flag", "team", "0.3.0", true, false, false},
		// An edition the manifest says nothing about must stay silent rather
		// than guess.
		{"unknown edition", "managed", "0.1.0", false, false, false},
		// A development build has no meaningful version; reporting it as out
		// of date would nag every developer on every run.
		{"dev build", "solo", "dev", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Compare(m, c.edition, c.current)
			if got.Available != c.available || got.Security != c.security || got.Unsupported != c.unsupported {
				t.Fatalf("Compare(%s, %s) = %+v; want available=%v security=%v unsupported=%v",
					c.edition, c.current, got, c.available, c.security, c.unsupported)
			}
		})
	}
}

func TestMessageLeadsWithSecurity(t *testing.T) {
	m := Manifest{"solo": {Latest: "0.4.0", Security: true}}
	msg := Compare(m, "solo", "0.3.0").Message()
	if !strings.HasPrefix(msg, "SECURITY:") {
		t.Fatalf("a security release must be unmistakable, got %q", msg)
	}
	if Compare(Manifest{"solo": {Latest: "0.4.0"}}, "solo", "0.4.0").Message() != "" {
		t.Fatal("an up-to-date instance must say nothing")
	}
}

// The privacy guarantee is the reason this package exists in this shape, so
// it is asserted rather than described: the request must carry no query
// string, no body, and nothing identifying the caller.
func TestFetchSendsNothingAboutTheInstance(t *testing.T) {
	var gotQuery, gotBody string
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotHeaders = r.Header.Clone()
		b := make([]byte, 1024)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.Write([]byte(`{"solo":{"latest":"0.4.0"}}`))
	}))
	defer srv.Close()

	m, err := Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if m["solo"].Latest != "0.4.0" {
		t.Fatalf("manifest not parsed: %+v", m)
	}
	if gotQuery != "" {
		t.Fatalf("request carried a query string: %q", gotQuery)
	}
	if gotBody != "" {
		t.Fatalf("request carried a body: %q", gotBody)
	}
	for _, h := range []string{"X-Rekam-Version", "X-Rekam-Edition", "X-Rekam-Instance", "Authorization", "Cookie"} {
		if v := gotHeaders.Get(h); v != "" {
			t.Fatalf("request carried %s: %q", h, v)
		}
	}
}

func TestFetchRejectsGarbage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>not json</html>"))
	}))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("want an error for a non-JSON manifest")
	}
}
