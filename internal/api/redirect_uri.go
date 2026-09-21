package api

import (
	"net"
	"net/url"
)

// redirectURIAllowed reports whether a client may receive an authorization
// code at this URI.
//
// Exact string comparison, with one exception required by RFC 8252 §7.3: a
// native app cannot know which loopback port it will get, so it registers
// one and listens on another. For http://127.0.0.1 and http://[::1] the port
// is therefore ignored and scheme, host and path must match. Everything else
// — hostname, scheme, path, query — must match exactly, because a laxer rule
// (prefix or suffix matching) is how redirect validation is usually defeated.
func redirectURIAllowed(registered []string, requested string) bool {
	req, err := url.Parse(requested)
	if err != nil {
		return false
	}
	for _, candidate := range registered {
		reg, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		if isLoopback(reg) && isLoopback(req) {
			if reg.Scheme == req.Scheme && reg.Path == req.Path &&
				reg.Hostname() == req.Hostname() {
				return true
			}
			continue
		}
		if candidate == requested {
			return true
		}
	}
	return false
}

// isLoopback reports whether a URI targets the machine the app runs on. Only
// literal loopback addresses count: "localhost" is deliberately excluded
// because it resolves through DNS and can be pointed elsewhere, which is the
// same reason RFC 8252 §8.3 tells native apps to use the literal addresses.
func isLoopback(u *url.URL) bool {
	if u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
