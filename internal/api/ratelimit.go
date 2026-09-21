package api

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultTrustedProxies is the peer set whose forwarding headers are believed
// when nothing else is configured: loopback only. That covers the two common
// shapes — a Cloudflare Tunnel, and a reverse proxy on the same host — while
// leaving a directly-exposed instance safe by default.
func defaultTrustedProxies() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
	}
}

// ParseTrustedProxies reads a comma-separated list of CIDRs or bare addresses
// (REKAM_TRUSTED_PROXIES) into a peer set. An operator whose reverse proxy is
// on another host needs this; without it every request through that proxy
// shares one rate-limit bucket, because they all arrive from the proxy's
// address.
func ParseTrustedProxies(spec string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, field := range strings.Split(spec, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if p, err := netip.ParsePrefix(field); err == nil {
			out = append(out, p)
			continue
		}
		addr, err := netip.ParseAddr(field)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q is not an IP or CIDR", field)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

// TrustProxies replaces the set of peers whose forwarding headers are
// believed. Call before Serve.
func (s *Server) TrustProxies(prefixes []netip.Prefix) { s.trustedProxies = prefixes }

// clientIP resolves the real client address for rate-limiting.
//
// Forwarding headers are only read when the immediate peer is one we trust to
// have set them. In the managed deployment that peer is cloudflared on
// loopback: the origin binds 127.0.0.1 and is reachable only through the
// tunnel, and Cloudflare overwrites CF-Connecting-IP with the true client
// address, so the header is worth believing.
//
// Believing it unconditionally is what this avoids, and the case that matters
// is not the managed one. A self-hosted instance listening on a LAN has no
// proxy in front and no Cloudflare anywhere: anything that can reach it could
// send a different CF-Connecting-IP on every request and get a fresh bucket
// each time, which costs the login throttle its entire purpose. The peer check
// is what makes the header's trustworthiness a property of the deployment
// rather than an assumption about it.
//
// Order, once the peer is trusted: CF-Connecting-IP, then the first
// X-Forwarded-For hop, then RemoteAddr.
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	if !peerIsTrusted(host, trusted) {
		return host
	}
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	return host
}

func peerIsTrusted(host string, trusted []netip.Prefix) bool {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// tokenBucket is a single key's allowance. tokens replenish continuously; each
// permitted request spends one.
type tokenBucket struct {
	tokens   float64
	lastFill time.Time
}

// rateLimiter is a per-key token-bucket limiter (keyed by client IP). The bucket
// holds up to burst tokens and refills at burst/window per unit time, so a key
// may burst up to `burst` requests, then sustains `burst` per `window`. Idle
// keys are evicted by a lazy sweep to bound memory — no background goroutine, so
// the limiter needs no shutdown wiring.
type rateLimiter struct {
	mu           sync.Mutex
	buckets      map[string]*tokenBucket
	burst        float64
	refillPerSec float64
	evictTTL     time.Duration
	lastSweep    time.Time
}

// newRateLimiter allows up to burst requests per key, refilling over window.
func newRateLimiter(burst int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		buckets:      make(map[string]*tokenBucket),
		burst:        float64(burst),
		refillPerSec: float64(burst) / window.Seconds(),
		// Keep a bucket around long enough that a full window of silence
		// (definitely refilled to full) has passed before dropping it.
		evictTTL: 10 * window,
	}
}

// allow reports whether the key may proceed. When denied it returns how long
// until one token is available, for a Retry-After hint.
func (rl *rateLimiter) allow(key string) (bool, time.Duration) {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.sweepLocked(now)

	b, ok := rl.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: rl.burst, lastFill: now}
		rl.buckets[key] = b
	} else {
		elapsed := now.Sub(b.lastFill).Seconds()
		b.tokens = math.Min(rl.burst, b.tokens+elapsed*rl.refillPerSec)
		b.lastFill = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1-b.tokens)/rl.refillPerSec*float64(time.Second)) + time.Second
	return false, wait
}

// sweepLocked drops buckets untouched for evictTTL. Called under mu, and rate-
// limited to once per evictTTL so it stays O(1) amortized on the hot path.
func (rl *rateLimiter) sweepLocked(now time.Time) {
	if now.Sub(rl.lastSweep) < rl.evictTTL {
		return
	}
	rl.lastSweep = now
	for k, b := range rl.buckets {
		if now.Sub(b.lastFill) > rl.evictTTL {
			delete(rl.buckets, k)
		}
	}
}

// rateLimit wraps a handler so each client IP is throttled by rl. Exceeding the
// limit returns 429 with a Retry-After header rather than reaching the handler.
// label identifies which limiter this is for rekam_ratelimit_rejections_total
// (see metrics.go) — a sustained climb there is the earliest signal of either
// abuse or a misbehaving client, well before it shows up as a support email.
func (s *Server) rateLimit(rl *rateLimiter, label string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := rl.allow(clientIP(r, s.trustedProxies)); !ok {
			rateLimitRejections.WithLabelValues(label).Inc()
			secs := max(int(math.Ceil(wait.Seconds())), 1)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			respond(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests — slow down and try again shortly"})
			return
		}
		next(w, r)
	}
}
