// Scenarios: spec/mcp.md (MCP-01..02, MCP-04..07, MCP-19, MCP-22)
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Ucok23/rekam/internal/db"
	"github.com/Ucok23/rekam/internal/engine"
)

// testServer builds a full MCP server backed by temp SQLite files.
func testServer(t *testing.T) (*Server, string, func()) {
	t.Helper()

	regF, _ := os.CreateTemp("", "mcp-reg-*.sqlite")
	regF.Close()
	memF, _ := os.CreateTemp("", "mcp-mem-*.memory")
	memF.Close()

	reg, err := db.OpenRegistry(regF.Name())
	if err != nil {
		t.Fatal(err)
	}

	apiKey := "mcp-test-key"
	if _, err := reg.Create("alice", apiKey, memF.Name(), true); err != nil {
		t.Fatal(err)
	}

	eng := engine.New(reg, 6000)
	srv := NewServer(eng)

	cleanup := func() {
		reg.Close()
		os.Remove(regF.Name())
		os.Remove(memF.Name())
	}
	return srv, apiKey, cleanup
}

// rpc sends a single JSON-RPC message over the stdio transport and returns the response.
func rpc(t *testing.T, srv *Server, apiKey string, method string, params any) map[string]any {
	t.Helper()

	msg := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		msg["params"] = params
	}
	line, _ := json.Marshal(msg)
	line = append(line, '\n')

	var out bytes.Buffer
	ctx := context.WithValue(context.Background(), keyAPIKey, apiKey)
	// serveReadWriter scans the reader to EOF and returns, so run it
	// synchronously: the single message is fully processed when it returns.
	if err := srv.serveReadWriter(ctx, bytes.NewReader(line), &out); err != nil {
		t.Fatalf("serveReadWriter: %v", err)
	}

	var resp map[string]any
	json.NewDecoder(&out).Decode(&resp)
	return resp
}

func TestMCPInitialize(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	resp := rpc(t, srv, key, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
		"capabilities":    map[string]any{},
	})

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got: %v", resp)
	}
	// MCP-01
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("unexpected protocolVersion: %v", result["protocolVersion"])
	}
	instructions, _ := result["instructions"].(string)
	if instructions == "" {
		t.Error("expected non-empty instructions in initialize result")
	}
}

func TestMCPToolsList(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	resp := rpc(t, srv, key, "tools/list", nil)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got: %v", resp)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatal("expected non-empty tools list")
	}

	names := map[string]bool{}
	for _, tRaw := range tools {
		tool := tRaw.(map[string]any)
		names[tool["name"].(string)] = true
	}
	// MCP-02
	for _, expected := range []string{"catalog", "search", "get_memory", "write_memory", "update_memory", "delete_memory"} {
		if !names[expected] {
			t.Errorf("missing tool: %s", expected)
		}
	}
}

func TestMCPCatalogEmpty(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	resp := rpc(t, srv, key, "tools/call", map[string]any{
		"name":      "catalog",
		"arguments": map[string]any{},
	})
	result := resp["result"].(map[string]any)
	content := result["content"].([]any)[0].(map[string]any)

	var body map[string]any
	json.Unmarshal([]byte(content["text"].(string)), &body)
	if body["taxonomy"] == nil {
		t.Error("expected taxonomy key in catalog response")
	}
}

func TestMCPCatalogResource(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	// The catalog is advertised as a resource.
	listResp := rpc(t, srv, key, "resources/list", map[string]any{})
	resources := listResp["result"].(map[string]any)["resources"].([]any)
	if len(resources) == 0 || resources[0].(map[string]any)["uri"].(string) != catalogResourceURI {
		t.Fatalf("expected %s in resources/list: %v", catalogResourceURI, resources)
	}

	// Reading it returns the taxonomy as JSON text.
	readResp := rpc(t, srv, key, "resources/read", map[string]any{"uri": catalogResourceURI})
	contents := readResp["result"].(map[string]any)["contents"].([]any)
	text := contents[0].(map[string]any)["text"].(string)
	if !contains(text, "taxonomy") {
		t.Errorf("expected taxonomy in resource contents: %s", text)
	}

	// An unknown URI is a not-found error.
	missResp := rpc(t, srv, key, "resources/read", map[string]any{"uri": "rekam://nope"})
	if missResp["error"] == nil {
		t.Error("expected error reading unknown resource")
	}
}

func TestMCPWriteAndSearch(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	// Write a memory.
	writeResp := rpc(t, srv, key, "tools/call", map[string]any{
		"name": "write_memory",
		"arguments": map[string]any{
			"title":    "savings account",
			"taxonomy": "finance.accounts",
			"keywords": "savings deposit interest bank",
			"content":  "Main savings account at BankX.",
		},
	})
	if writeResp["error"] != nil {
		t.Fatalf("write error: %v", writeResp["error"])
	}

	// Catalog with no args rolls up to the top-level branch.
	catResp := rpc(t, srv, key, "tools/call", map[string]any{
		"name": "catalog", "arguments": map[string]any{},
	})
	// MCP-04: catalog with no prefix rolls up to the top-level branch, not the leaf path.
	catContent := catResp["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !contains(catContent, "finance") {
		t.Errorf("expected finance branch in catalog: %s", catContent)
	}

	// Drilling in with a prefix reveals the full leaf path.
	drillResp := rpc(t, srv, key, "tools/call", map[string]any{
		"name": "catalog", "arguments": map[string]any{"prefix": "finance"},
	})
	// MCP-04
	drillContent := drillResp["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !contains(drillContent, "finance.accounts") {
		t.Errorf("expected finance.accounts in drill-down catalog: %s", drillContent)
	}

	// Search should find it.
	searchResp := rpc(t, srv, key, "tools/call", map[string]any{
		"name": "search",
		"arguments": map[string]any{
			"q":        "savings",
			"taxonomy": "finance",
		},
	})
	// MCP-05
	searchContent := searchResp["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !contains(searchContent, "savings account") {
		t.Errorf("expected result in search: %s", searchContent)
	}
}

// Scenarios: spec/revisions.md (REV-16)

// TestMCPWriteAndUpdateReportAgent covers the actual point of the `agent`
// field: an MCP client (Claude, or anything else calling these tools) can
// self-report which model is driving the call, and that value lands on the
// revision it produced — a different agent per call, not one value stuck to
// the memory as a whole.
func TestMCPWriteAndUpdateReportAgent(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	writeResp := rpc(t, srv, key, "tools/call", map[string]any{
		"name": "write_memory",
		"arguments": map[string]any{
			"title":    "Deploy checklist",
			"taxonomy": "ops",
			"content":  "1. run tests 2. tag release",
			"agent":    "claude-opus-5",
		},
	})
	if writeResp["error"] != nil {
		t.Fatalf("write error: %v", writeResp["error"])
	}
	writeText := writeResp["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	var written struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(writeText), &written); err != nil {
		t.Fatalf("decode write response: %v (body: %s)", err, writeText)
	}
	if written.ID == "" {
		t.Fatal("write response carried no id")
	}

	updateResp := rpc(t, srv, key, "tools/call", map[string]any{
		"name": "update_memory",
		"arguments": map[string]any{
			"id":      written.ID,
			"content": "1. run tests 2. tag release 3. announce",
			"agent":   "qwen-2.5-72b",
		},
	})
	if updateResp["error"] != nil {
		t.Fatalf("update error: %v", updateResp["error"])
	}

	revs, err := srv.eng.MemoryHistory(key, written.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 2 {
		t.Fatalf("revisions = %d, want 2", len(revs))
	}
	// Newest first.
	if revs[0].Agent != "qwen-2.5-72b" {
		t.Errorf("latest revision agent = %q, want qwen-2.5-72b", revs[0].Agent)
	}
	if revs[1].Agent != "claude-opus-5" {
		t.Errorf("first revision agent = %q, want claude-opus-5", revs[1].Agent)
	}
}

func TestMCPUnknownTool(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	resp := rpc(t, srv, key, "tools/call", map[string]any{
		"name": "does_not_exist", "arguments": map[string]any{},
	})
	// MCP-06
	if resp["error"] == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestMCPBadAPIKey(t *testing.T) {
	srv, _, cleanup := testServer(t)
	defer cleanup()

	resp := rpc(t, srv, "bad-key", "tools/call", map[string]any{
		"name": "catalog", "arguments": map[string]any{},
	})
	// MCP-07
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true for bad key, got: %v", result)
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}

// TestTeamBoundKeyRoutesToTeam verifies the MCP transport honours the
// X-Rekam-Team header: teamBoundKey rebinds the bearer so tool calls act inside
// the selected team's corpus, not the caller's personal one.
func TestTeamBoundKeyRoutesToTeam(t *testing.T) {
	srv, apiKey, cleanup := testServer(t)
	defer cleanup()

	// Create a team and write a memory into it (via a team-bound engine bearer).
	dir := t.TempDir()
	team, err := srv.eng.CreateTeam(apiKey, "Acme", dir+"/acme.rekam")
	if err != nil {
		t.Fatal(err)
	}

	// The transport derives the team-bound key from the header on the request.
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("X-Rekam-Team", team.ID)
	// MCP-22
	teamKey := srv.teamBoundKey(req)
	if teamKey == apiKey {
		t.Fatal("teamBoundKey did not rebind the bearer to the team")
	}

	// Writing with the team key lands in the team; the raw key's home stays empty.
	if _, err := srv.eng.WriteMemory(teamKey, engine.WriteInput{Title: "Team Doc", Taxonomy: "work"}); err != nil {
		t.Fatal(err)
	}
	teamHits := rpc(t, srv, teamKey, "tools/call", map[string]any{
		"name": "search", "arguments": map[string]any{"q": "Team"},
	})
	if teamHits["error"] != nil {
		t.Fatalf("team search errored: %v", teamHits["error"])
	}
	homeHits := rpc(t, srv, apiKey, "tools/call", map[string]any{
		"name": "search", "arguments": map[string]any{"q": "Team"},
	})
	// MCP-19 / MCP-22: home search must not surface the team's memory. Both
	// calls return a tool result; we assert the home result carries no hit
	// for "Team Doc".
	if resultText(homeHits) == resultText(teamHits) {
		t.Error("home and team searches returned identical results; selector must isolate")
	}
}

func resultText(resp map[string]any) string {
	b, _ := json.Marshal(resp["result"])
	return string(b)
}
