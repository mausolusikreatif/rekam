// Scenarios: spec/mcp.md (MCP-20..26)
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// httpRPC posts a JSON-RPC message to the MCP HTTP transport the way a remote
// client (claude.ai) does, and returns the raw recorder.
func httpRPC(t *testing.T, srv *Server, path, bearer string, msg any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if msg != nil {
		body, _ = json.Marshal(msg)
	}
	r := httptest.NewRequest("POST", path, bytes.NewReader(body))
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	r.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	srv.SSEHandler().ServeHTTP(rr, r)
	return rr
}

func decodeRPC(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response (%d): %v: %s", rr.Code, err, rr.Body)
	}
	return resp
}

// A remote client connecting over Streamable HTTP must be able to complete a
// whole session on that transport alone: handshake, discover the tools, then use
// them. This is the path claude.ai takes, so it has to work end to end.
// MCP-21
func TestRemoteClientCompletesASessionOverHTTP(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	// Handshake.
	rr := httpRPC(t, srv, "/mcp", key, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": protocolVersion},
	}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("initialize: want 200, got %d: %s", rr.Code, rr.Body)
	}
	init := decodeRPC(t, rr)
	result, ok := init["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize returned no result: %v", init)
	}
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v, want %s", result["protocolVersion"], protocolVersion)
	}
	// The server hands the client a session id it can echo back.
	if rr.Header().Get("Mcp-Session-Id") == "" {
		t.Error("initialize should assign an Mcp-Session-Id")
	}

	// Discover the tools.
	tools := decodeRPC(t, httpRPC(t, srv, "/mcp", key, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	}, nil))
	list, _ := tools["result"].(map[string]any)["tools"].([]any)
	if len(list) == 0 {
		t.Fatalf("tools/list returned nothing: %v", tools)
	}

	// Use one, and read the record back on the same transport.
	write := decodeRPC(t, httpRPC(t, srv, "/mcp", key, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{
			"name": "write_memory",
			"arguments": map[string]any{
				"title": "Remote Note", "taxonomy": "work.ops", "content": "written over HTTP",
			},
		},
	}, nil))
	if write["error"] != nil {
		t.Fatalf("write over HTTP: %v", write["error"])
	}

	search := decodeRPC(t, httpRPC(t, srv, "/mcp", key, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "search", "arguments": map[string]any{"q": "Remote"}},
	}, nil))
	text := search["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "Remote Note") {
		t.Errorf("the record written over HTTP should be findable: %s", text)
	}
}

// The Bearer token is re-checked on every request, not just at handshake — a
// remote connection is stateless, so an unauthenticated call must never slip
// through on the strength of an earlier one.
// MCP-21
func TestRemoteTransportChecksCredentialsOnEveryRequest(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	// A good handshake first.
	if rr := httpRPC(t, srv, "/mcp", key, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	}, nil); rr.Code != http.StatusOK {
		t.Fatalf("initialize: %d: %s", rr.Code, rr.Body)
	}

	// A later call with no Bearer is refused at the transport.
	if rr := httpRPC(t, srv, "/mcp", "", map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	}, nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("missing Bearer: want 401, got %d: %s", rr.Code, rr.Body)
	}

	// A syntactically valid but unknown key gets past the transport and is
	// refused by the tool, in-band, with a reason.
	resp := decodeRPC(t, httpRPC(t, srv, "/mcp", "not-a-real-key", map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "catalog", "arguments": map[string]any{}},
	}, nil))
	if result, ok := resp["result"].(map[string]any); !ok || result["isError"] != true {
		t.Errorf("an unknown key must not be served: %v", resp)
	}
}

// A remote client that selects a workspace gets that workspace for the whole
// connection, and cannot reach another one by asking.
// MCP-22
func TestRemoteTransportHonoursTheWorkspaceSelector(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	team, err := srv.eng.CreateTeam(key, "Acme", t.TempDir()+"/acme.rekam")
	if err != nil {
		t.Fatal(err)
	}

	// Write into the team by selecting it on the request.
	write := decodeRPC(t, httpRPC(t, srv, "/mcp", key, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "write_memory",
			"arguments": map[string]any{"title": "Acme Runbook", "taxonomy": "work.ops"},
		},
	}, map[string]string{"X-Rekam-Team": team.ID}))
	if write["error"] != nil {
		t.Fatalf("team write: %v", write["error"])
	}

	inTeam := decodeRPC(t, httpRPC(t, srv, "/mcp", key, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "search", "arguments": map[string]any{"q": "Acme"}},
	}, map[string]string{"X-Rekam-Team": team.ID}))
	teamText := inTeam["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(teamText, "Acme Runbook") {
		t.Errorf("the selected workspace should hold the record: %s", teamText)
	}

	// The same connection without the selector sees personal memory instead.
	atHome := decodeRPC(t, httpRPC(t, srv, "/mcp", key, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "search", "arguments": map[string]any{"q": "Acme"}},
	}, nil))
	homeText := atHome["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if strings.Contains(homeText, "Acme Runbook") {
		t.Errorf("personal memory must not show the workspace's records: %s", homeText)
	}
}

// Notifications carry no id and expect no answer; the transport must accept them
// quietly rather than replying with a malformed response.
// MCP-20
func TestRemoteTransportAcknowledgesNotifications(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	rr := httpRPC(t, srv, "/mcp", key, map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	}, nil)
	if rr.Code != http.StatusAccepted {
		t.Errorf("a notification should be accepted with 202, got %d: %s", rr.Code, rr.Body)
	}
	if strings.TrimSpace(rr.Body.String()) != "" {
		t.Errorf("a notification must not get a response body: %s", rr.Body)
	}
}

// Malformed input and unknown methods come back as proper JSON-RPC errors, so a
// confused client can recover instead of hanging on a broken payload.
// MCP-26
func TestRemoteTransportReportsProtocolErrors(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	// Not JSON at all.
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader("{not json"))
	r.Header.Set("Authorization", "Bearer "+key)
	rr := httptest.NewRecorder()
	srv.SSEHandler().ServeHTTP(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unparseable body: want 400, got %d", rr.Code)
	}
	if decodeRPC(t, rr)["error"] == nil {
		t.Error("a parse failure should be reported as a JSON-RPC error")
	}

	// A method the server does not implement.
	resp := decodeRPC(t, httpRPC(t, srv, "/mcp", key, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "does/not/exist",
	}, nil))
	if resp["error"] == nil {
		t.Errorf("an unknown method should be an error: %v", resp)
	}

	// A route the transport does not serve.
	req := httptest.NewRequest("GET", "/mcp/nope", nil)
	notFound := httptest.NewRecorder()
	srv.SSEHandler().ServeHTTP(notFound, req)
	if notFound.Code != http.StatusNotFound {
		t.Errorf("unknown transport path: want 404, got %d", notFound.Code)
	}
}

// Browser-based clients preflight before connecting; a rejected preflight means
// the connection never happens.
// MCP-25
func TestRemoteTransportAnswersCORSPreflight(t *testing.T) {
	srv, _, cleanup := testServer(t)
	defer cleanup()

	req := httptest.NewRequest("OPTIONS", "/mcp", nil)
	rr := httptest.NewRecorder()
	srv.SSEHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("preflight: want 204, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("preflight must allow the origin")
	}
	allowed := rr.Header().Get("Access-Control-Allow-Headers")
	for _, h := range []string{"Authorization", "X-Rekam-Team"} {
		if !strings.Contains(allowed, h) {
			t.Errorf("preflight must permit the %s header, got %q", h, allowed)
		}
	}
}

// The older SSE transport is a two-part conversation: the client opens a stream,
// is told where to post, and its replies come back down the stream. Clients still
// on that transport must be able to complete a call.
// MCP-23
func TestLegacySSEClientReceivesItsRepliesOnTheStream(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	// A real connection, so the long-lived stream behaves as it does in
	// production rather than as a buffer the test also holds.
	http.DefaultClient.CloseIdleConnections()
	ts := httptest.NewServer(srv.SSEHandler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/mcp/sse", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want an event stream", ct)
	}

	// Read the stream on its own goroutine; the test consumes lines from a
	// channel so nothing is shared but the channel itself.
	lines := make(chan string, 32)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()

	nextLine := func(what string) string {
		t.Helper()
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed while waiting for %s", what)
			}
			return line
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s", what)
			return ""
		}
	}

	// The server announces where to post.
	if line := nextLine("the endpoint event"); line != "event: endpoint" {
		t.Fatalf("first event = %q, want the endpoint announcement", line)
	}
	endpoint := strings.TrimPrefix(nextLine("the endpoint URL"), "data: ")
	if !strings.Contains(endpoint, "/mcp/message?session_id=") {
		t.Fatalf("endpoint = %q, want a session-bound message URL", endpoint)
	}

	// Post a call there. The HTTP reply is only an ack — the real answer comes
	// back down the stream, which is the whole shape of this transport.
	post, err := http.Post(endpoint, "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatalf("post message: %v", err)
	}
	defer post.Body.Close()
	if post.StatusCode != http.StatusAccepted {
		t.Fatalf("message post: want 202, got %d", post.StatusCode)
	}

	// Skip blank separators until the message event arrives.
	var event string
	for range 5 {
		if event = nextLine("the message event"); event == "event: message" {
			break
		}
	}
	if event != "event: message" {
		t.Fatalf("expected a message event on the stream, got %q", event)
	}
	payload := strings.TrimPrefix(nextLine("the message payload"), "data: ")
	if !strings.Contains(payload, "write_memory") {
		t.Errorf("the streamed reply should carry the tools list: %s", payload)
	}
	var reply map[string]any
	if err := json.Unmarshal([]byte(payload), &reply); err != nil {
		t.Fatalf("streamed reply is not JSON-RPC: %v: %s", err, payload)
	}
	if reply["id"] != float64(1) {
		t.Errorf("the reply should carry the request id, got %v", reply["id"])
	}
}

// An SSE stream requires credentials, and a message posted against a session
// that was never opened must be refused rather than falling back to anonymous.
// MCP-24
func TestLegacySSERejectsUnauthenticatedAndUnknownSessions(t *testing.T) {
	srv, _, cleanup := testServer(t)
	defer cleanup()

	anon := httptest.NewRequest("GET", "/mcp/sse", nil)
	rr := httptest.NewRecorder()
	srv.SSEHandler().ServeHTTP(rr, anon)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("opening a stream without a Bearer: want 401, got %d", rr.Code)
	}

	post := httptest.NewRequest("POST", "/mcp/message?session_id=never-existed",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	postRR := httptest.NewRecorder()
	srv.SSEHandler().ServeHTTP(postRR, post)
	if postRR.Code != http.StatusUnauthorized {
		t.Errorf("posting to an unknown session: want 401, got %d: %s", postRR.Code, postRR.Body)
	}
}
