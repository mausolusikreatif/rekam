// Package updatecheck tells an operator that a newer rekam exists.
//
// It is deliberately the smallest thing that can do that. A self-hosted
// instance had no way to learn a new build had shipped, which also meant no
// way to be told about a security fix — that is the problem being solved,
// not "keeping installs current".
//
// # What this sends
//
// Nothing. The request is a plain GET of a static manifest with no query
// parameters, no headers identifying the instance, and no body. The current
// version and edition are never transmitted; the comparison happens locally
// after the manifest arrives. An observer at the other end learns only that
// some IP fetched a public file, which is the same thing they learn from
// anyone loading the website.
//
// That property is why this is a separate package with a narrow surface: it
// is easy to check by reading it, and hard to erode by accident. Anything
// that would send instance data belongs in a different package with a
// different name, so nobody adds it here believing it is still "just the
// update check".
//
// It never updates anything. Replacing a binary underneath a running server
// is the operator's decision, and a self-hoster's existing binary keeps
// working indefinitely regardless of entitlement.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultManifestURL is the published manifest. Overridable with
// REKAM_UPDATE_URL, which exists for tests and for anyone mirroring releases
// internally — not as a general configuration knob.
const DefaultManifestURL = "https://rekam.net/version.json"

// Release is what the manifest says about one edition.
type Release struct {
	Latest string `json:"latest"`
	// MinSupported names a floor below which versions are known-bad, so an
	// advisory can call out a range instead of leaving operators to infer it.
	MinSupported string `json:"min_supported"`
	// Security marks a release that fixes a vulnerability. This is the field
	// the whole feature exists for: it decides whether the operator sees a
	// warning or a passing note.
	Security bool   `json:"security"`
	Notes    string `json:"notes"`
}

// Manifest is keyed by edition ("solo", "team", "managed", "desktop"), since
// editions ship on their own cadences.
type Manifest map[string]Release

// Result is the local comparison. Zero value means "nothing to say".
type Result struct {
	Available   bool
	Security    bool
	Unsupported bool
	Current     string
	Latest      string
	Notes       string
}

// Message renders the one line an operator sees, or "" when up to date.
func (r Result) Message() string {
	switch {
	case r.Unsupported:
		s := fmt.Sprintf("this version (%s) is below the supported floor; upgrade to %s", r.Current, r.Latest)
		if r.Notes != "" {
			s += " — " + r.Notes
		}
		return s
	case r.Available && r.Security:
		s := fmt.Sprintf("SECURITY: %s is available and fixes a vulnerability (running %s)", r.Latest, r.Current)
		if r.Notes != "" {
			s += " — " + r.Notes
		}
		return s
	case r.Available:
		return fmt.Sprintf("rekam %s is available (running %s)", r.Latest, r.Current)
	}
	return ""
}

// Fetch retrieves the manifest. The request carries nothing about the caller.
func Fetch(ctx context.Context, url string) (Manifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest: HTTP %d", resp.StatusCode)
	}
	// Bounded: a manifest is a few hundred bytes, and this runs unattended.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	return m, nil
}

// Compare works out what to say about `current` given the manifest entry for
// `edition`. An unknown edition or an unparseable version says nothing —
// silence is right for a check nobody asked for.
func Compare(m Manifest, edition, current string) Result {
	rel, ok := m[edition]
	if !ok || rel.Latest == "" {
		return Result{}
	}
	res := Result{Current: current, Latest: rel.Latest, Notes: rel.Notes}
	if cmp, ok := compareVersions(current, rel.Latest); ok && cmp < 0 {
		res.Available = true
		res.Security = rel.Security
	}
	if rel.MinSupported != "" {
		if cmp, ok := compareVersions(current, rel.MinSupported); ok && cmp < 0 {
			res.Unsupported = true
		}
	}
	return res
}

// compareVersions does a semver-ish comparison, enough for vX.Y.Z tags. The
// bool reports whether both parsed; a development build ("dev") does not, and
// must not be reported as out of date.
func compareVersions(a, b string) (int, bool) {
	pa, ok := parse(a)
	if !ok {
		return 0, false
	}
	pb, ok := parse(b)
	if !ok {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// Ignore any -rc / +meta suffix; a prerelease compares as its base.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
