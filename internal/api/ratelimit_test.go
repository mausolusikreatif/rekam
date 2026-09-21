package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterBurstThenDeny(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)
	for i := range 3 {
		if ok, _ := rl.allow("1.2.3.4"); !ok {
			t.Fatalf("request %d within burst should be allowed", i+1)
		}
	}
	ok, wait := rl.allow("1.2.3.4")
	if ok {
		t.Fatal("4th request over burst should be denied")
	}
	if wait <= 0 {
		t.Fatalf("denied request should report a positive Retry-After, got %v", wait)
	}
}

func TestRateLimiterKeysAreIndependent(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	if ok, _ := rl.allow("1.1.1.1"); !ok {
		t.Fatal("first key should be allowed")
	}
	if ok, _ := rl.allow("2.2.2.2"); !ok {
		t.Fatal("a different key must not share the first key's bucket")
	}
	if ok, _ := rl.allow("1.1.1.1"); ok {
		t.Fatal("first key is now exhausted and should be denied")
	}
}

func TestRateLimiterRefills(t *testing.T) {
	// 60 tokens/min = 1 token/sec. Drain, then advance the bucket's clock.
	rl := newRateLimiter(60, time.Minute)
	if ok, _ := rl.allow("k"); !ok {
		t.Fatal("first request should pass")
	}
	// Force the bucket empty and rewind lastFill 2s so refill yields ~2 tokens.
	rl.mu.Lock()
	b := rl.buckets["k"]
	b.tokens = 0
	b.lastFill = time.Now().Add(-2 * time.Second)
	rl.mu.Unlock()
	if ok, _ := rl.allow("k"); !ok {
		t.Fatal("after ~2s of refill at 1 token/sec the request should pass")
	}
}

func TestClientIPPrefersCloudflareHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/ui/login", nil)
	r.RemoteAddr = "127.0.0.1:5000" // loopback: the tunnel origin
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 10.0.0.1")
	r.Header.Set("CF-Connecting-IP", "203.0.113.7")
	if got := clientIP(r, defaultTrustedProxies()); got != "203.0.113.7" {
		t.Fatalf("want CF-Connecting-IP, got %q", got)
	}
}

func TestClientIPFallsBackToForwardedFirstHop(t *testing.T) {
	r := httptest.NewRequest("POST", "/ui/login", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 10.0.0.1")
	if got := clientIP(r, defaultTrustedProxies()); got != "9.9.9.9" {
		t.Fatalf("want first XFF hop, got %q", got)
	}
}

// An untrusted peer's forwarding headers are ignored entirely. Without this,
// anything that can reach a directly-exposed instance gets a fresh rate-limit
// bucket per request just by varying a header — which is the whole of the
// login throttle.
func TestClientIPIgnoresHeadersFromAnUntrustedPeer(t *testing.T) {
	r := httptest.NewRequest("POST", "/ui/login", nil)
	r.RemoteAddr = "198.51.100.4:44444" // straight off the internet, no proxy
	r.Header.Set("CF-Connecting-IP", "203.0.113.7")
	r.Header.Set("X-Forwarded-For", "9.9.9.9")
	if got := clientIP(r, defaultTrustedProxies()); got != "198.51.100.4" {
		t.Fatalf("an untrusted peer must be bucketed by its own address, got %q", got)
	}

	// Two forged values must not become two buckets.
	r.Header.Set("CF-Connecting-IP", "203.0.113.8")
	if got := clientIP(r, defaultTrustedProxies()); got != "198.51.100.4" {
		t.Fatalf("varying the header must not change the bucket, got %q", got)
	}
}

// An operator whose proxy is on another host says so, and then it is believed.
func TestClientIPTrustsAConfiguredProxy(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8, 192.168.1.5")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/ui/login", nil)
	r.RemoteAddr = "10.4.0.9:5000"
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 10.0.0.1")
	if got := clientIP(r, trusted); got != "9.9.9.9" {
		t.Fatalf("a configured proxy's XFF should be believed, got %q", got)
	}

	// A bare address is trusted exactly, and its neighbour is not.
	r.RemoteAddr = "192.168.1.5:5000"
	if got := clientIP(r, trusted); got != "9.9.9.9" {
		t.Fatalf("a bare trusted address should be believed, got %q", got)
	}
	r.RemoteAddr = "192.168.1.6:5000"
	if got := clientIP(r, trusted); got != "192.168.1.6" {
		t.Fatalf("an address next to a trusted one is not trusted, got %q", got)
	}

	if _, err := ParseTrustedProxies("not-an-ip"); err == nil {
		t.Error("a malformed trusted-proxy entry should be an error, not silently dropped")
	}
}

func TestClientIPFallsBackToRemoteAddr(t *testing.T) {
	r := httptest.NewRequest("POST", "/ui/login", nil)
	r.RemoteAddr = "198.51.100.4:44444"
	if got := clientIP(r, defaultTrustedProxies()); got != "198.51.100.4" {
		t.Fatalf("want RemoteAddr host, got %q", got)
	}
}
