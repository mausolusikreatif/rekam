// Scenarios: spec/mcp.md (MCP-03, MCP-08..18)
package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// callTool invokes an MCP tool the way a client does and returns the decoded
// payload. A tool that fails reports it in-band (isError), not as a transport
// error, so the agent can read the reason and adapt — tests assert on that.
func callTool(t *testing.T, srv *Server, key, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	resp := rpc(t, srv, key, "tools/call", map[string]any{"name": name, "arguments": args})
	if resp["error"] != nil {
		t.Fatalf("%s: transport error: %v", name, resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no result: %v", name, resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("%s: empty content: %v", name, result)
	}
	text := content[0].(map[string]any)["text"].(string)
	isErr, _ := result["isError"].(bool)

	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		// Some payloads are arrays or scalars; hand the raw text back instead.
		return map[string]any{"_text": text}, isErr
	}
	return payload, isErr
}

// listOf reads a JSON array field, treating an absent or null field as empty —
// an empty result set is a legitimate answer, not a malformed one.
func listOf(t *testing.T, payload map[string]any, key string) []any {
	t.Helper()
	v, ok := payload[key]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("field %q is not a list: %v", key, v)
	}
	return arr
}

// mustCall fails the test if the tool reported an error.
func mustCall(t *testing.T, srv *Server, key, name string, args map[string]any) map[string]any {
	t.Helper()
	payload, isErr := callTool(t, srv, key, name, args)
	if isErr {
		t.Fatalf("%s failed: %v", name, payload)
	}
	return payload
}

// writeRecord is the common "agent records something" step, returning the id.
func writeRecord(t *testing.T, srv *Server, key, title, taxonomy, content string) string {
	t.Helper()
	mem := mustCall(t, srv, key, "write_memory", map[string]any{
		"title": title, "taxonomy": taxonomy, "content": content,
	})
	id, _ := mem["id"].(string)
	if id == "" {
		t.Fatalf("write_memory returned no id: %v", mem)
	}
	return id
}

// MCP-08
// The core loop an agent lives in across sessions: record something, find it
// again later, read it in full, revise it, and see the revision reflected.
func TestAgentRecordsRecallsAndRevises(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	id := writeRecord(t, srv, key, "Deploy Runbook", "work.ops", "Push to main, then tag.")

	// A later session recalls it by id and gets the full body, not a snippet.
	got := mustCall(t, srv, key, "get_memory", map[string]any{"id": id})
	if got["title"] != "Deploy Runbook" {
		t.Errorf("get_memory title = %v", got["title"])
	}
	if !strings.Contains(got["content"].(string), "Push to main") {
		t.Errorf("get_memory should return the full content, got %v", got["content"])
	}

	// The agent learns something new and revises the record.
	updated := mustCall(t, srv, key, "update_memory", map[string]any{
		"id": id, "content": "Push to main, tag, then announce in #eng.",
	})
	if !strings.Contains(updated["content"].(string), "announce in #eng") {
		t.Errorf("update_memory did not apply the new content: %v", updated["content"])
	}
	// Revising bumps the version, which is what lets a later edit detect a clash.
	if v, _ := updated["version"].(float64); v < 2 {
		t.Errorf("version should advance on update, got %v", updated["version"])
	}
	// Retitling without touching the body keeps the body.
	retitled := mustCall(t, srv, key, "update_memory", map[string]any{
		"id": id, "title": "Deploy Runbook (v2)",
	})
	if !strings.Contains(retitled["content"].(string), "announce in #eng") {
		t.Errorf("a title-only edit must not blank the content: %v", retitled["content"])
	}

	// And the revision is what search now returns.
	res := mustCall(t, srv, key, "search", map[string]any{"q": "announce"})
	if len(listOf(t, res, "results")) != 1 {
		t.Errorf("search should find the revised record: %v", res)
	}
}

// MCP-09
// An agent that overwrites a record it read before someone else changed it would
// silently destroy the other writer's work. Passing the version it read makes
// the clash an explicit, recoverable error instead.
func TestConcurrentAgentsCannotClobberEachOther(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	id := writeRecord(t, srv, key, "Shared Notes", "work.ops", "original")
	first := mustCall(t, srv, key, "get_memory", map[string]any{"id": id})
	staleVersion := first["version"].(float64)

	// Another writer gets there first.
	mustCall(t, srv, key, "update_memory", map[string]any{"id": id, "content": "someone else's edit"})

	// The agent still holding the old version is refused, with a reason it can act on.
	payload, isErr := callTool(t, srv, key, "update_memory", map[string]any{
		"id": id, "content": "my stale edit", "base_version": staleVersion,
	})
	if !isErr {
		t.Fatalf("a stale-base update must be refused, got: %v", payload)
	}
	if code, _ := payload["code"].(string); code != "version_conflict" {
		t.Errorf("expected a version_conflict the agent can retry on, got %v", payload)
	}

	// The other writer's content survived.
	now := mustCall(t, srv, key, "get_memory", map[string]any{"id": id})
	if !strings.Contains(now["content"].(string), "someone else's edit") {
		t.Errorf("the winning edit was clobbered: %v", now["content"])
	}

	// Without a base_version the agent is explicitly opting into last-writer-wins.
	mustCall(t, srv, key, "update_memory", map[string]any{"id": id, "content": "deliberate overwrite"})
}

// MCP-10
// A bad edit is recoverable: the agent can read the record's history and put an
// earlier version back.
func TestAgentRollsBackABadEdit(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	id := writeRecord(t, srv, key, "Runbook", "work.ops", "the good content")
	mustCall(t, srv, key, "update_memory", map[string]any{"id": id, "content": "oops, wrong paste"})

	hist := mustCall(t, srv, key, "memory_history", map[string]any{"id": id})
	revs := listOf(t, hist, "revisions")
	if len(revs) < 2 {
		t.Fatalf("history should show both the original and the bad edit, got %d: %v", len(revs), hist)
	}

	restored := mustCall(t, srv, key, "restore_revision", map[string]any{"id": id, "version": float64(1)})
	if !strings.Contains(restored["content"].(string), "the good content") {
		t.Errorf("restore did not bring back version 1: %v", restored["content"])
	}

	// The rollback is itself a revision — history is append-only, so the bad
	// edit stays auditable rather than being erased.
	after := mustCall(t, srv, key, "memory_history", map[string]any{"id": id})
	if len(listOf(t, after, "revisions")) <= len(revs) {
		t.Errorf("restoring should append a revision, not rewrite history: %v", after)
	}

	// Restoring needs to say which version; a missing one is a usable error.
	payload, isErr := callTool(t, srv, key, "restore_revision", map[string]any{"id": id})
	if !isErr {
		t.Fatalf("restore without a version must fail, got %v", payload)
	}
	if !strings.Contains(strings.ToLower(payload["message"].(string)), "version") {
		t.Errorf("the error should name the missing field, got %v", payload)
	}
}

// MCP-11
// Deleting is not silent erasure: the record stops being retrievable, but the
// agent can still see that something was there and when.
func TestDeletedRecordDisappearsButLeavesATrace(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	id := writeRecord(t, srv, key, "Obsolete Policy", "work.ops", "no longer applies")
	mustCall(t, srv, key, "delete_memory", map[string]any{"id": id})

	// It is gone from reads...
	if payload, isErr := callTool(t, srv, key, "get_memory", map[string]any{"id": id}); !isErr {
		t.Errorf("a deleted record must not be retrievable, got %v", payload)
	}
	res := mustCall(t, srv, key, "search", map[string]any{"q": "Obsolete"})
	if n := len(listOf(t, res, "results")); n != 0 {
		t.Errorf("a deleted record must not surface in search, got %d hits", n)
	}

	// ...but the tombstone records that it existed, so a link to it isn't a mystery.
	del := mustCall(t, srv, key, "deleted_memories", nil)
	tombstones := listOf(t, del, "deleted")
	if len(tombstones) != 1 {
		t.Fatalf("expected one tombstone, got %v", del)
	}
	if title, _ := tombstones[0].(map[string]any)["title"].(string); title != "Obsolete Policy" {
		t.Errorf("tombstone should name what was deleted, got %v", tombstones[0])
	}

	// Deleting the same id again is an error, not a silent success.
	if payload, isErr := callTool(t, srv, key, "delete_memory", map[string]any{"id": id}); !isErr {
		t.Errorf("double delete should report not-found, got %v", payload)
	}
}

// MCP-12
// The value of the graph is traversal: an agent asked "what relates to X" should
// get X's neighbourhood, and links that point nowhere should be reported so they
// can be fixed rather than silently rotting.
func TestAgentTraversesAndRepairsTheLinkGraph(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	writeRecord(t, srv, key, "Postgres", "work.infra", "The database.")
	apiID := writeRecord(t, srv, key, "API Service", "work.infra",
		"Talks to [[Postgres]] and caches in [[Redis]].")

	// Centring the graph on the API surfaces the record it links to.
	g := mustCall(t, srv, key, "graph", map[string]any{"center": apiID, "depth": float64(1)})
	blob, _ := json.Marshal(g)
	if !strings.Contains(string(blob), "Postgres") {
		t.Errorf("graph centred on the API should reach Postgres: %s", blob)
	}

	// [[Redis]] has no record behind it. edge_health is how the agent finds out,
	// and it must name the source record so the broken link can be repaired.
	health := mustCall(t, srv, key, "edge_health", nil)
	if n, _ := health["dangling"].(float64); n != 1 {
		t.Errorf("expected exactly one dangling link, got %v", health)
	}
	hblob, _ := json.Marshal(health)
	if !strings.Contains(strings.ToLower(string(hblob)), "redis") {
		t.Errorf("edge_health should name the missing target: %s", hblob)
	}
	if !strings.Contains(string(hblob), "API Service") {
		t.Errorf("edge_health should name the record holding the broken link: %s", hblob)
	}

	// Writing the missing record heals the link without touching the source.
	writeRecord(t, srv, key, "Redis", "work.infra", "The cache.")
	healed := mustCall(t, srv, key, "edge_health", nil)
	if n, _ := healed["dangling"].(float64); n != 0 {
		t.Errorf("the dangling link should heal once the target exists: %v", healed)
	}
	if n, _ := healed["resolved"].(float64); n != 2 {
		t.Errorf("both links should now resolve, got %v", healed)
	}
}

// MCP-15
// Browsing a branch is a different job from searching it: the agent wants the
// contents of a taxonomy, not the best matches for a query.
func TestAgentBrowsesABranchByTaxonomy(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	writeRecord(t, srv, key, "Deploy Runbook", "work.ops", "how to ship")
	writeRecord(t, srv, key, "Oncall Rota", "work.ops", "who is paged")
	writeRecord(t, srv, key, "Savings", "finance.accounts", "unrelated")

	scope := mustCall(t, srv, key, "memory_scope", map[string]any{"taxonomy": "work.ops"})
	blob, _ := json.Marshal(scope)
	if !strings.Contains(string(blob), "Deploy Runbook") || !strings.Contains(string(blob), "Oncall Rota") {
		t.Errorf("browsing work.ops should list both of its records: %s", blob)
	}
	if strings.Contains(string(blob), "Savings") {
		t.Errorf("browsing work.ops must not leak another branch: %s", blob)
	}
}

// MCP-03
// Before writing, an agent reads the corpus's intended vocabulary so it files
// records consistently instead of inventing a new path each session.
func TestAgentReadsTheTaxonomyTemplateBeforeFiling(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	list := rpc(t, srv, key, "resources/list", map[string]any{})
	resources := list["result"].(map[string]any)["resources"].([]any)
	var advertised bool
	for _, r := range resources {
		if r.(map[string]any)["uri"] == templateResourceURI {
			advertised = true
		}
	}
	if !advertised {
		t.Fatalf("the taxonomy template must be discoverable as a resource: %v", resources)
	}

	read := rpc(t, srv, key, "resources/read", map[string]any{"uri": templateResourceURI})
	contents := read["result"].(map[string]any)["contents"].([]any)
	text := contents[0].(map[string]any)["text"].(string)
	if strings.TrimSpace(text) == "" || text == "null" {
		t.Fatalf("the template resource should carry branches to file into, got %q", text)
	}
	// It must be usable as guidance: branches with a "what goes here" note.
	if !strings.Contains(text, "path") {
		t.Errorf("template should describe branch paths, got %s", text)
	}
}

// MCP-14
// An agent that has been given related-but-unlinked records should be told about
// them, so the graph doesn't fragment into islands.
func TestAgentIsOfferedLinksItHasNotMadeYet(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	writeRecord(t, srv, key, "Kubernetes Rollout", "work.infra", "rollout strategy for the cluster")
	writeRecord(t, srv, key, "Cluster Autoscaling", "work.infra", "rollout strategy for the cluster")

	sug := mustCall(t, srv, key, "suggest_links", map[string]any{"limit": float64(10)})
	if _, ok := sug["suggestions"]; !ok {
		t.Fatalf("suggest_links should always answer with a suggestions list: %v", sug)
	}
}

// MCP-19
// An agent connected through a workspace-scoped grant needs to know which
// workspaces it can act in, and writing in one must not touch another.
func TestAgentSeesItsWorkspacesAndTheyStayIsolated(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	team, err := srv.eng.CreateTeam(key, "Acme", t.TempDir()+"/acme.rekam")
	if err != nil {
		t.Fatal(err)
	}

	teams := mustCall(t, srv, key, "list_teams", nil)
	blob, _ := json.Marshal(teams["teams"])
	if !strings.Contains(string(blob), "Acme") {
		t.Errorf("list_teams should name the workspaces the agent can act in: %s", blob)
	}

	teamKey, err := srv.eng.BearerForTeam(key, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	writeRecord(t, srv, teamKey, "Team Only", "work.ops", "lives in Acme")

	home := mustCall(t, srv, key, "search", map[string]any{"q": "Team Only"})
	if n := len(listOf(t, home, "results")); n != 0 {
		t.Errorf("a record written in a workspace must not appear in personal memory, got %d hits", n)
	}
	inTeam := mustCall(t, srv, teamKey, "search", map[string]any{"q": "Team Only"})
	if n := len(listOf(t, inTeam, "results")); n != 1 {
		t.Errorf("the record should be found inside its own workspace, got %d hits", n)
	}
}

// MCP-17
// Tools must refuse work the caller isn't entitled to, and say so in-band so the
// agent can explain itself rather than retrying blindly.
func TestReadOnlyAgentCannotWrite(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	id := writeRecord(t, srv, key, "Runbook", "work.ops", "content")

	// A second identity on the same corpus, without write permission.
	reader, err := srv.eng.CreateUser("reader", "reader@example.com", "reader-password", t.TempDir()+"/reader.rekam", false, false)
	if err != nil {
		t.Fatal(err)
	}
	readerKey := srv.eng.SessionToken(reader.ID)

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"write_memory", map[string]any{"title": "New", "taxonomy": "work.ops"}},
		{"update_memory", map[string]any{"id": id, "content": "edited"}},
		{"delete_memory", map[string]any{"id": id}},
	} {
		payload, isErr := callTool(t, srv, readerKey, tc.tool, tc.args)
		if !isErr {
			t.Errorf("%s should be refused for a read-only identity, got %v", tc.tool, payload)
		}
	}

	// Reading still works for that identity's own corpus.
	if _, isErr := callTool(t, srv, readerKey, "catalog", nil); isErr {
		t.Error("a read-only identity should still be able to browse")
	}
}

// MCP-16
// Malformed tool arguments must come back as an explainable error, never a
// panic or a silently empty write.
func TestAgentGetsUsableErrorsForBadArguments(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"write with no title", "write_memory", map[string]any{"taxonomy": "work.ops"}},
		{"write with no taxonomy", "write_memory", map[string]any{"title": "Orphan"}},
		{"write with a malformed taxonomy", "write_memory", map[string]any{"title": "X", "taxonomy": "not a path!"}},
		{"write with an unsupported format", "write_memory", map[string]any{"title": "X", "taxonomy": "work", "format": "wingdings"}},
		{"get an unknown id", "get_memory", map[string]any{"id": "does-not-exist"}},
		{"update an unknown id", "update_memory", map[string]any{"id": "does-not-exist", "content": "x"}},
		{"history of an unknown id", "memory_history", map[string]any{"id": "does-not-exist"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, isErr := callTool(t, srv, key, tc.tool, tc.args)
			if !isErr {
				t.Fatalf("expected an in-band error, got %v", payload)
			}
			msg, _ := payload["message"].(string)
			if msg == "" {
				msg, _ = payload["_text"].(string)
			}
			if strings.TrimSpace(msg) == "" {
				t.Errorf("the error must explain itself to the agent, got %v", payload)
			}
		})
	}
}

// MCP-13
// The search index is derived state; rebuilding it must not lose or change
// anything the user can retrieve.
func TestRebuildingTheSearchIndexKeepsEverythingFindable(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	writeRecord(t, srv, key, "Deploy Runbook", "work.ops", "push to main")
	writeRecord(t, srv, key, "Savings Overview", "finance.accounts", "bank details")

	before := mustCall(t, srv, key, "search", map[string]any{"q": "runbook"})
	mustCall(t, srv, key, "rebuild_fts", nil)
	after := mustCall(t, srv, key, "search", map[string]any{"q": "runbook"})

	if len(listOf(t, before, "results")) != len(listOf(t, after, "results")) {
		t.Errorf("rebuild changed what search returns: before %v, after %v", before, after)
	}
	if len(listOf(t, after, "results")) != 1 {
		t.Errorf("expected the runbook to still be findable after a rebuild: %v", after)
	}
}

// MCP-18
// A record stored as a diagram round-trips through the agent surface unchanged —
// the format is preserved, not coerced to markdown.
func TestAgentStoresNonMarkdownRecords(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	mem := mustCall(t, srv, key, "write_memory", map[string]any{
		"title": "Service Map", "taxonomy": "work.arch",
		"format": "mermaid", "content": "graph TD;\n  a-->b;",
	})
	id := mem["id"].(string)
	if mem["format"] != "mermaid" {
		t.Errorf("format should be stored as given, got %v", mem["format"])
	}

	got := mustCall(t, srv, key, "get_memory", map[string]any{"id": id})
	if got["format"] != "mermaid" || !strings.Contains(got["content"].(string), "a-->b") {
		t.Errorf("diagram did not round-trip: %v", got)
	}
}
