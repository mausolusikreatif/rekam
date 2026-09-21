package updatecheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The manifest that actually ships. Compare() is deliberately silent about
// anything it cannot make sense of — an unknown edition, an unparseable
// version — which is right at runtime but means a malformed manifest fails by
// saying nothing at all. A security release that announces itself to nobody is
// the exact failure this feature exists to prevent, so the file is checked
// here instead.
func TestPublishedManifestIsUsable(t *testing.T) {
	path := filepath.Join("..", "..", "release", "version.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s does not parse as a manifest: %v", path, err)
	}
	if len(m) == 0 {
		t.Fatal("the manifest is empty; no instance would ever hear about a release")
	}

	known := map[string]bool{"solo": true, "team": true, "managed": true, "desktop": true}
	for edition, rel := range m {
		if !known[edition] {
			t.Errorf("edition %q is not one any binary asks for — Compare would ignore it silently", edition)
		}
		if _, ok := parse(rel.Latest); !ok {
			t.Errorf("%s: latest %q is not a vX.Y.Z version, so no comparison can happen", edition, rel.Latest)
		}
		if rel.MinSupported != "" {
			if _, ok := parse(rel.MinSupported); !ok {
				t.Errorf("%s: min_supported %q is not a version", edition, rel.MinSupported)
			} else if cmp, _ := compareVersions(rel.Latest, rel.MinSupported); cmp < 0 {
				t.Errorf("%s: latest %s is below min_supported %s — every install would be told it is unsupported, including the newest",
					edition, rel.Latest, rel.MinSupported)
			}
		}
		if rel.Security && rel.Notes == "" {
			t.Errorf("%s: a security release should carry notes saying what to do", edition)
		}

		// The end-to-end property: an older install of this edition is
		// actually told something.
		if got := Compare(m, edition, "v0.0.1").Message(); got == "" {
			t.Errorf("%s: an install on v0.0.1 is told nothing", edition)
		}
		// And the current one is not nagged.
		if got := Compare(m, edition, rel.Latest).Message(); got != "" {
			t.Errorf("%s: an install already on %s is told %q", edition, rel.Latest, got)
		}
	}
}
