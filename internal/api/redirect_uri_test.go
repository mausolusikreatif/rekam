package api

import "testing"

func TestRedirectURIAllowed(t *testing.T) {
	cases := []struct {
		name       string
		registered []string
		requested  string
		want       bool
	}{
		{"exact match", []string{"https://claude.ai/api/mcp/auth_callback"},
			"https://claude.ai/api/mcp/auth_callback", true},
		{"unregistered host", []string{"https://claude.ai/cb"},
			"https://evil.example/cb", false},

		// RFC 8252 §7.3: a native app registers one loopback port and listens
		// on another, so the port is ignored — this is what the desktop
		// client depends on.
		{"loopback, any port", []string{"http://127.0.0.1:0/callback"},
			"http://127.0.0.1:41234/callback", true},
		{"loopback ipv6", []string{"http://[::1]:0/callback"},
			"http://[::1]:9999/callback", true},
		{"loopback, different path", []string{"http://127.0.0.1:0/callback"},
			"http://127.0.0.1:41234/steal", false},

		// The exemption must not become a hole. localhost resolves through
		// DNS and can be repointed, so it is not loopback here (RFC 8252 §8.3).
		{"localhost is not loopback", []string{"http://127.0.0.1:0/callback"},
			"http://localhost:41234/callback", false},
		{"loopback registration does not license a remote host",
			[]string{"http://127.0.0.1:0/callback"}, "http://evil.example/callback", false},
		{"https loopback-looking host is still exact-match",
			[]string{"https://127.0.0.1/cb"}, "https://127.0.0.1:8443/cb", false},

		// The classic ways redirect validation gets defeated.
		{"prefix is not a match", []string{"https://claude.ai/cb"},
			"https://claude.ai/cb/../../evil", false},
		{"suffix is not a match", []string{"https://claude.ai/cb"},
			"https://evil.example/?x=https://claude.ai/cb", false},
		{"subdomain is not a match", []string{"https://claude.ai/cb"},
			"https://evil.claude.ai/cb", false},
		{"added query is not a match", []string{"https://claude.ai/cb"},
			"https://claude.ai/cb?next=https://evil.example", false},

		{"no registration allows nothing", nil, "https://claude.ai/cb", false},
		{"unparseable request", []string{"https://claude.ai/cb"}, "://nope", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := redirectURIAllowed(c.registered, c.requested); got != c.want {
				t.Fatalf("redirectURIAllowed(%q, %q) = %v, want %v",
					c.registered, c.requested, got, c.want)
			}
		})
	}
}
