package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/Ucok23/rekam/internal/engine"
	"github.com/google/uuid"
)

const protocolVersion = "2024-11-05"

// contextKey is the key type for context values.
type contextKey string

const keyAPIKey contextKey = "api_key"

// session holds per-SSE-connection state.
type session struct {
	apiKey string
	send   chan []byte
	done   chan struct{}
}

// Server is an MCP server backed by the engine.
// For SSE transport it maintains a session store so the API key only needs to
// be presented once — at the initial GET /mcp/sse connection.
type Server struct {
	eng      *engine.Engine
	tools    []Tool
	sessions sync.Map // session_id(string) -> *session
}

// NewServer creates a new MCP server.
func NewServer(eng *engine.Engine) *Server {
	s := &Server{eng: eng}
	s.tools = s.buildTools()
	return s
}

// ServeStdio runs the MCP stdio transport (for Claude Desktop).
// The API key is read from REKAM_API_KEY or the --key flag.
func (s *Server) ServeStdio(apiKey string) error {
	ctx := context.WithValue(context.Background(), keyAPIKey, apiKey)
	return s.serveReadWriter(ctx, os.Stdin, os.Stdout)
}

// SSEHandler returns an http.Handler that serves MCP over both transports:
//   - Streamable HTTP (POST /mcp) — used by claude.ai and modern clients
//   - SSE transport (GET /mcp/sse + POST /mcp/message) — used by older clients
func (s *Server) SSEHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/mcp")
		switch {
		case (path == "" || path == "/sse") && r.Method == http.MethodGet:
			s.handleSSE(w, r)
		case path == "/message" && r.Method == http.MethodPost:
			s.handleMessage(w, r)
		case path == "" && r.Method == http.MethodPost:
			s.handleStreamableHTTP(w, r)
		case r.Method == http.MethodOptions:
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Session-Id, X-Rekam-Team")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
}

// ── stdio transport ───────────────────────────────────────────────────────────

func (s *Server) serveReadWriter(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4<<20), 4<<20) // 4 MiB
	enc := json.NewEncoder(w)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			enc.Encode(errResp(nil, CodeParse, "parse error", nil))
			continue
		}

		// Notifications have no id — no response required.
		if msg.ID == nil {
			continue
		}

		result, rpcErr := s.dispatch(ctx, msg)
		if rpcErr != nil {
			enc.Encode(Response{JSONRPC: "2.0", ID: msg.ID, Error: rpcErr})
			continue
		}
		enc.Encode(Response{JSONRPC: "2.0", ID: msg.ID, Result: result})
	}
	return scanner.Err()
}

// ── SSE transport ─────────────────────────────────────────────────────────────

// handleSSE establishes a long-lived SSE connection.
// The API key is read once here and bound to a session.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	apiKey := s.teamBoundKey(r)
	if apiKey == "" {
		http.Error(w, "Authorization: Bearer <key> required", http.StatusUnauthorized)
		return
	}

	sessionID := uuid.New().String()
	sess := &session{apiKey: apiKey, send: make(chan []byte, 16), done: make(chan struct{})}
	s.sessions.Store(sessionID, sess)
	defer func() {
		s.sessions.Delete(sessionID)
		close(sess.done)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, canFlush := w.(http.Flusher)

	// Send the message endpoint URL with the session ID embedded.
	// Use X-Forwarded-Proto when behind a reverse proxy (e.g. Cloudflare Tunnel).
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	endpoint := fmt.Sprintf("%s://%s/mcp/message?session_id=%s", scheme, r.Host, sessionID)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpoint)
	if canFlush {
		flusher.Flush()
	}

	// Forward responses from handleMessage back to the client over the stream.
	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-sess.send:
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

// handleMessage receives a single JSON-RPC request from the SSE client.
// The session_id query param is used to resolve the API key.
// Fallback: if no session_id, the Authorization header is checked (useful
// for non-SSE clients and testing).
func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Rekam-Team")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Resolve API key: session first, Authorization header as fallback.
	var sess *session
	apiKey := ""
	if sid := r.URL.Query().Get("session_id"); sid != "" {
		if v, ok := s.sessions.Load(sid); ok {
			sess = v.(*session)
			apiKey = sess.apiKey
		} else {
			writeJSON(w, http.StatusUnauthorized, errResp(nil, CodeInvalid, "unknown or expired session_id", nil))
			return
		}
	} else {
		apiKey = s.teamBoundKey(r)
	}

	ctx := context.WithValue(r.Context(), keyAPIKey, apiKey)

	var msg Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp(nil, CodeParse, "parse error", nil))
		return
	}

	// Notifications have no id — no response required.
	if msg.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var result any
	var rpcErr *RPCError
	result, rpcErr = s.dispatch(ctx, msg)
	var resp Response
	if rpcErr != nil {
		resp = Response{JSONRPC: "2.0", ID: msg.ID, Error: rpcErr}
	} else {
		resp = Response{JSONRPC: "2.0", ID: msg.ID, Result: result}
	}

	// Send response on the SSE stream if we have a live session,
	// otherwise fall back to the HTTP response body (e.g. direct curl tests).
	if sess != nil {
		b, _ := json.Marshal(resp)
		select {
		case sess.send <- b:
			w.WriteHeader(http.StatusAccepted)
		case <-sess.done:
			http.Error(w, "session closed", http.StatusGone)
		}
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleStreamableHTTP serves the MCP Streamable HTTP transport (2025-03-26 spec).
// Each POST to /mcp is a self-contained JSON-RPC request; the Bearer token is
// validated on every request. Used by claude.ai and other modern MCP clients.
func (s *Server) handleStreamableHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	apiKey := s.teamBoundKey(r)
	if apiKey == "" {
		http.Error(w, "Authorization: Bearer <key> required", http.StatusUnauthorized)
		return
	}

	var msg Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp(nil, CodeParse, "parse error", nil))
		return
	}

	// Notifications have no id — acknowledge with 202.
	if msg.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	ctx := context.WithValue(r.Context(), keyAPIKey, apiKey)
	result, rpcErr := s.dispatch(ctx, msg)

	resp := Response{JSONRPC: "2.0", ID: msg.ID}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}

	// Assign a session ID on initialize so the client can include it in subsequent
	// requests (we don't actually require it — each request re-validates the Bearer token).
	if msg.Method == "initialize" {
		w.Header().Set("Mcp-Session-Id", uuid.New().String())
	}

	writeJSON(w, http.StatusOK, resp)
}

// ── dispatch ──────────────────────────────────────────────────────────────────

func (s *Server) dispatch(ctx context.Context, msg Message) (any, *RPCError) {
	switch msg.Method {
	case "initialize":
		return s.handleInitialize(ctx, msg)
	case "tools/list":
		return ToolsListResult{Tools: s.tools}, nil
	case "tools/call":
		return s.handleToolCall(ctx, msg)
	case "resources/list":
		return ResourcesListResult{Resources: s.resources()}, nil
	case "resources/read":
		return s.handleResourceRead(ctx, msg)
	case "ping":
		return map[string]any{}, nil
	default:
		return nil, &RPCError{Code: CodeNotFound, Message: "method not found: " + msg.Method}
	}
}

func (s *Server) handleInitialize(ctx context.Context, _ Message) (any, *RPCError) {
	instructions := mcpInstructions

	if key := apiKey(ctx); key != "" {
		if result, err := s.eng.GetSkillsManifest(key); err == nil && len(result.Results) > 0 {
			var sb strings.Builder
			fmt.Fprintf(&sb, "\n\n## Skills\nYou have %d skill(s) defined. Before starting any task, load applicable ones with get_memory.\n", len(result.Results))
			for _, sk := range result.Results {
				fmt.Fprintf(&sb, "\n- id:%s  [%s]  %s", sk.ID, sk.Taxonomy, sk.Title)
			}
			instructions += sb.String()
		}
	}

	return InitializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities: Capabilities{
			Tools:     &ToolsCapability{},
			Resources: &ResourcesCapability{},
		},
		ServerInfo:   ServerInfo{Name: "rekam", Version: "1.0.0"},
		Instructions: instructions,
	}, nil
}

const mcpInstructions = `You have access to a persistent memory store via MCP tools.

## Retrieval
1. If you already know the taxonomy, skip straight to ` + "`search`" + ` with that prefix.
2. Otherwise call ` + "`catalog`" + ` to see top-level branches, then call it again with a ` + "`prefix`" + ` to drill into branches marked ` + "`expandable`" + `.
3. ` + "`search`" + ` with q + taxonomy; if count is small, ` + "`memory_scope`" + ` to browse titles first.
4. If omitted_count > 0, narrow the query before concluding.
5. Use ` + "`get_memory`" + ` when a snippet is insufficient.

` + "`catalog`" + ` is for discovery — don't re-call it when the taxonomy is already known. Avoid ` + "`search`" + ` without a ` + "`taxonomy`" + ` unless the question spans all memory.

The top-level tree is also published as an MCP resource at ` + "`rekam://catalog`" + ` (` + "`application/json`" + `, same shape as the ` + "`catalog`" + ` tool with no args). If your client supports resources, read ` + "`rekam://catalog`" + ` for the top-level branches instead of calling the ` + "`catalog`" + ` tool — resource reads can be cached/subscribed, so the tree isn't re-injected into context every turn. Still use the ` + "`catalog`" + ` tool with a ` + "`prefix`" + ` to drill into a branch; the resource only carries the top level.

## Writing
1. Classify taxonomy (dot-notation: work.projects, finance.savings). Read the ` + "`rekam://taxonomy-template`" + ` resource first — it lists the branches this corpus expects, each with a "what goes here" note. File into an existing template branch when one fits, rather than inventing a parallel path; extend the tree only when nothing fits.
2. Write a concise, scannable title
3. Write content in markdown with enough context for a future reader
4. Link related memories inline (see Linking) — an unlinked memory is an island; linking is how recall stays connected, so treat it as part of writing, not an extra
5. Call ` + "`write_memory`" + ` and check the returned ` + "`links`" + ` array — each entry shows whether that ` + "`[[link]]`" + ` resolved; ` + "`resolved:false`" + ` means the title didn't match (fix a typo, or it's intentionally dangling until the target exists)

## Linking
You can only link to memories you know exist. Before writing, link from titles already in your context from this session's ` + "`search`" + `/` + "`catalog`" + ` results; if you're writing cold with nothing related in context, run one ` + "`memory_scope`" + ` on the target taxonomy (titles only — cheap) to see what's there, then link the relevant ones by exact title.

Connect memories with inline ` + "`[[Title]]`" + ` wiki-links in content — the text matches a memory's title (case-insensitive). Add a relation prefix to shape ranking:
- ` + "`[[Title]]`" + ` or ` + "`[[relates::Title]]`" + ` — generic association; boosts the target's authority. An unknown prefix (e.g. ` + "`[[needs::X]]`" + `) degrades to a plain ` + "`relates`" + ` link.
- ` + "`[[depends-on::Title]]`" + ` — this memory builds on / requires the target; boosts it.
- ` + "`[[supersedes::Title]]`" + ` — this memory replaces an older one. The superseded memory is demoted in search and returned flagged ` + "`superseded`" + `. Use this to retire a stale record instead of leaving a confusing duplicate — it is the main tool for keeping outdated context out of results.
- ` + "`[[contradicts::Title]]`" + ` — conflicts with the target; surfaced but not penalized.

Link intentionally, only where there is a real connection — do not bracket every keyword. Links to a not-yet-existing title are kept and resolve automatically once that memory is created. A link whose target was deleted comes back flagged ` + "`deleted`" + ` rather than unresolved: it stays bound to the removed memory permanently, so it never re-points at a different memory that later takes the same title. Use ` + "`deleted_memories`" + ` to see what happened to it. On retrieval, ` + "`search`" + ` hits carry ` + "`backlinks`" + ` (authority) and ` + "`superseded`" + `; ` + "`get_memory`" + ` returns ` + "`linked_from`" + ` with each link's relation — follow these to traverse related context.

## Updating
Use ` + "`update_memory`" + ` to patch fields (the link graph re-syncs automatically). Use ` + "`delete_memory`" + ` to remove. Prefer ` + "`supersedes`" + ` over deleting when a record is merely outdated.

Every write is versioned. When you edit a memory you have read, pass its ` + "`version`" + ` back as ` + "`base_version`" + `: if someone else wrote in the meantime the update fails with ` + "`version_conflict`" + ` (carrying ` + "`current_version`" + `) instead of overwriting them — re-read, reapply your change, and retry. Use ` + "`memory_history`" + ` to see prior versions with their authors, and ` + "`restore_revision`" + ` to roll one back. Nothing is lost on overwrite, so history is the way to recover text an edit removed.`

func (s *Server) handleToolCall(ctx context.Context, msg Message) (any, *RPCError) {
	var p ToolCallParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return nil, &RPCError{Code: CodeParams, Message: "invalid params: " + err.Error()}
	}

	handler, ok := s.toolHandler(p.Name)
	if !ok {
		return nil, &RPCError{Code: CodeNotFound, Message: "tool not found: " + p.Name}
	}

	result, err := handler(ctx, p.Arguments)
	if err != nil {
		if ae, ok := errors.AsType[*engine.ActionableError](err); ok {
			b, _ := json.Marshal(ae)
			return ToolResult{IsError: true, Content: []Content{{Type: "text", Text: string(b)}}}, nil
		}
		return ToolResult{IsError: true, Content: []Content{{Type: "text", Text: err.Error()}}}, nil
	}
	return result, nil
}

// ── tool registry ─────────────────────────────────────────────────────────────

type handlerFn func(ctx context.Context, args map[string]any) (ToolResult, error)

type toolEntry struct {
	tool    Tool
	handler handlerFn
}

var toolEntries []toolEntry

func (s *Server) buildTools() []Tool {
	toolEntries = []toolEntry{
		{
			tool: Tool{
				Name:        "catalog",
				Description: "Browse the taxonomy as a shallow tree. With no args, returns top-level branches with memory counts. Pass a `prefix` to drill into a branch (entries with expandable=true have deeper sub-paths). Use to discover where to scope a search.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]Property{
						"prefix": {Type: "string", Description: "Taxonomy branch to drill into, e.g. 'work'. Omit for top-level."},
						"depth":  {Type: "number", Description: "Segments to roll up to (0 = one level below prefix)."},
					},
				},
				Annotations: &Annotations{ReadOnlyHint: true},
			},
			handler: s.catalogHandler,
		},
		{
			tool: Tool{
				Name:        "search",
				Description: "Full-text search across memories, ranked by relevance blended with link authority. Scope with a taxonomy prefix from catalog results. Check omitted_count — if >0, narrow your query. Hits include backlinks (how many memories link here) and superseded (true if a newer memory replaced this one).",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]Property{
						"q":            {Type: "string", Description: "FTS5 search query (required)"},
						"taxonomy":     {Type: "string", Description: "Taxonomy prefix to scope search, e.g. 'finance'"},
						"token_budget": {Type: "number", Description: "Max tokens to return (0 = server default)"},
					},
					Required: []string{"q"},
				},
				Annotations: &Annotations{ReadOnlyHint: true},
			},
			handler: s.searchHandler,
		},
		{
			tool: Tool{
				Name:        "get_memory",
				Description: "Fetch a single memory by ID, including its full content and linked_from (memories that link to it, each tagged with its relation).",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]Property{
						"id": {Type: "string", Description: "Memory UUID"},
					},
					Required: []string{"id"},
				},
				Annotations: &Annotations{ReadOnlyHint: true},
			},
			handler: s.getMemoryHandler,
		},
		{
			tool: Tool{
				Name:        "write_memory",
				Description: "Create a new memory. Write a good title and content in markdown. Link related memories inline with [[Title]] wiki-links, optionally typed: [[depends-on::Title]], [[supersedes::Title]] (retires a stale memory), [[contradicts::Title]]. You can only link to titles that exist — memory_scope the taxonomy first if you don't already know them. The response includes a `links` array reporting which links resolved (resolved:false = dangling, usually a typo'd title).",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]Property{
						"title":    {Type: "string", Description: "Concise, scannable title"},
						"taxonomy": {Type: "string", Description: "Dot-notation path, e.g. 'finance.accounts'"},
						"content":  {Type: "string", Description: "Content body (text). Interpreted per 'format'."},
						"format":   {Type: "string", Description: "Content type: markdown (default), mermaid, code, or table. Use mermaid for diagrams."},
						"agent":    {Type: "string", Description: "The model or system making this write, e.g. 'claude-opus-5', 'qwen-2.5-72b'. Self-reported — pass your own identity here so provenance is visible in the memory's history. Optional, but always include it."},
					},
					Required: []string{"title", "taxonomy"},
				},
				Annotations: &Annotations{DestructiveHint: true},
			},
			handler: s.writeMemoryHandler,
		},
		{
			tool: Tool{
				Name:        "update_memory",
				Description: "Update fields on an existing memory. Pass only the fields you want to change.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]Property{
						"id":           {Type: "string", Description: "Memory UUID (required)"},
						"title":        {Type: "string", Description: "New title"},
						"content":      {Type: "string", Description: "New content body (text). Interpreted per 'format'."},
						"taxonomy":     {Type: "string", Description: "New taxonomy path (dot-notation)"},
						"format":       {Type: "string", Description: "New content type: markdown, mermaid, code, or table."},
						"base_version": {Type: "number", Description: "The `version` you read before editing. If someone else has written since, the update is rejected with version_conflict instead of overwriting them. Omit only when you have not read the memory first."},
						"agent":        {Type: "string", Description: "The model or system making this write, e.g. 'claude-opus-5', 'qwen-2.5-72b'. Self-reported — pass your own identity here so provenance is visible in the memory's history. Optional, but always include it."},
					},
					Required: []string{"id"},
				},
				Annotations: &Annotations{DestructiveHint: true},
			},
			handler: s.updateMemoryHandler,
		},
		{
			tool: Tool{
				Name:        "list_teams",
				Description: "List the workspaces (personal memory plus shared team corpora) the current connection can act in, with your role in each. This connection is pinned to one workspace, chosen when it was authorized, and every tool call reads and writes that one — it cannot be switched from here. To work in a different workspace, authorize a second connection and pick it at the consent screen. Clients that set headers directly can instead select a workspace with X-Rekam-Team.",
				InputSchema: InputSchema{
					Type:       "object",
					Properties: map[string]Property{},
				},
				Annotations: &Annotations{ReadOnlyHint: true},
			},
			handler: s.listTeamsHandler,
		},
		{
			tool: Tool{
				Name:        "deleted_memories",
				Description: "List deleted memories: what was removed, by whom, and when. Deletion is terminal — the content and history are purged and the id is permanently reserved — so these tombstones are the only remaining trace. Use to answer \"what happened to X\" when an id or link no longer resolves.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]Property{
						"taxonomy": {Type: "string", Description: "Restrict to a taxonomy prefix"},
						"limit":    {Type: "number", Description: "Maximum tombstones to return"},
					},
				},
				Annotations: &Annotations{ReadOnlyHint: true},
			},
			handler: s.deletedMemoriesHandler,
		},
		{
			tool: Tool{
				Name:        "memory_history",
				Description: "List a memory's revision history, newest first. Each entry is the full state at that version, with the author and timestamp. Use to see what changed, recover text an edit removed, or find the version to restore.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]Property{
						"id": {Type: "string", Description: "Memory UUID"},
					},
					Required: []string{"id"},
				},
				Annotations: &Annotations{ReadOnlyHint: true},
			},
			handler: s.memoryHistoryHandler,
		},
		{
			tool: Tool{
				Name:        "restore_revision",
				Description: "Roll a memory back to an earlier version, copying that revision's fields forward as a new version. History is append-only, so the restore is itself recorded and can be undone the same way.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]Property{
						"id":      {Type: "string", Description: "Memory UUID"},
						"version": {Type: "number", Description: "Version number to restore, as reported by memory_history"},
					},
					Required: []string{"id", "version"},
				},
				Annotations: &Annotations{DestructiveHint: true},
			},
			handler: s.restoreRevisionHandler,
		},
		{
			tool: Tool{
				Name:        "delete_memory",
				Description: "Permanently delete a memory by ID. This is irreversible and destroys the full revision history: the content cannot be recovered, and the id is reserved forever so it can never be restored or reused. A tombstone recording the title, who deleted it, and when is all that remains. Prefer `supersedes` when a record is merely outdated — that keeps it readable while demoting it in search. If other memories still link here, the delete is refused (has_inbound_links) unless force is set, since it would otherwise leave those links dangling with no way back.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]Property{
						"id":    {Type: "string", Description: "Memory UUID"},
						"force": {Type: "boolean", Description: "Delete anyway even if other memories link here (default false)"},
					},
					Required: []string{"id"},
				},
				Annotations: &Annotations{DestructiveHint: true},
			},
			handler: s.deleteMemoryHandler,
		},
		{
			tool: Tool{
				Name:        "memory_scope",
				Description: "List memories within a taxonomy prefix — returns id and title only (no content). Use to browse before searching.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]Property{
						"taxonomy":     {Type: "string", Description: "Taxonomy prefix, e.g. 'work.projects'"},
						"token_budget": {Type: "number", Description: "Max tokens to return (default 2000)"},
					},
					Required: []string{"taxonomy"},
				},
				Annotations: &Annotations{ReadOnlyHint: true},
			},
			handler: s.memoryScopeHandler,
		},
		{
			tool: Tool{
				Name:        "rebuild_fts",
				Description: "Rebuild the FTS5 search index from scratch.",
				InputSchema: InputSchema{Type: "object"},
				Annotations: &Annotations{DestructiveHint: true},
			},
			handler: s.rebuildFTSHandler,
		},
		{
			tool: Tool{
				Name:        "edge_health",
				Description: "Link-graph readout: total/resolved/dangling [[link]] counts plus the top unresolved targets. High dangling = links are being made but titles mistyped (or point at not-yet-written memories); near-zero total = linking is being skipped.",
				InputSchema: InputSchema{Type: "object"},
				Annotations: &Annotations{ReadOnlyHint: true},
			},
			handler: s.edgeHealthHandler,
		},
		{
			tool: Tool{
				Name:        "graph",
				Description: "Traverse the typed [[wiki-link]] graph. With `center` (a memory id), returns that memory's neighborhood out to `depth` hops (default 2) over relates/depends-on/supersedes/contradicts edges, both directions. Without `center`, returns the whole link graph, optionally scoped to a `taxonomy` prefix. Dangling links appear as nodes with dangling:true. Use to see how memories connect, find clusters, or pull a depends-on subtree.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]Property{
						"center":   {Type: "string", Description: "Memory id to center the ego graph on; omit for the whole corpus"},
						"depth":    {Type: "number", Description: "Ego-graph hop radius (default 2, max 4); ignored without center"},
						"taxonomy": {Type: "string", Description: "Restrict a corpus graph to this taxonomy prefix; ignored with center"},
					},
				},
				Annotations: &Annotations{ReadOnlyHint: true},
			},
			handler: s.graphHandler,
		},
		{
			tool: Tool{
				Name:        "suggest_links",
				Description: "Find memory pairs that look related (strong text overlap) but aren't yet connected by a [[wiki-link]] — the inverse of dangling links. Use to discover and fill missing connections in the graph. Returns source/target pairs with a similarity score; add the link by editing the source's content with [[Target Title]].",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]Property{
						"limit": {Type: "number", Description: "Max suggestions to return (default 20)"},
					},
				},
				Annotations: &Annotations{ReadOnlyHint: true},
			},
			handler: s.suggestLinksHandler,
		},
	}

	tools := make([]Tool, len(toolEntries))
	for i, e := range toolEntries {
		tools[i] = e.tool
	}
	return tools
}

func (s *Server) toolHandler(name string) (handlerFn, bool) {
	for _, e := range toolEntries {
		if e.tool.Name == name {
			return e.handler, true
		}
	}
	return nil, false
}

// ── tool handlers ─────────────────────────────────────────────────────────────

// catalogResourceURI is the cacheable top-level taxonomy tree. Clients can read it
// once and subscribe instead of re-injecting the catalog into context on every call.
const catalogResourceURI = "rekam://catalog"

// templateResourceURI is the corpus's taxonomy template: the branches this
// corpus expects records to be filed into, with a "what goes here" note each.
// An agent reads it to classify into a fixed vocabulary rather than inventing
// paths per session. Unlike the catalog (what exists), this is what's intended.
const templateResourceURI = "rekam://taxonomy-template"

func (s *Server) resources() []Resource {
	return []Resource{
		{
			URI:         catalogResourceURI,
			Name:        "Taxonomy catalog",
			Description: "Top-level taxonomy branches with memory counts. Drill in with the catalog tool.",
			MimeType:    "application/json",
		},
		{
			URI:         templateResourceURI,
			Name:        "Taxonomy template",
			Description: "The corpus's intended taxonomy: branches with descriptions to classify new records into. Read before writing to file consistently.",
			MimeType:    "application/json",
		},
	}
}

func (s *Server) handleResourceRead(ctx context.Context, msg Message) (any, *RPCError) {
	var p ResourcesReadParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return nil, &RPCError{Code: CodeParams, Message: "invalid params: " + err.Error()}
	}
	var payload any
	switch p.URI {
	case catalogResourceURI:
		entries, err := s.eng.Catalog(apiKey(ctx), "", 0)
		if err != nil {
			return nil, &RPCError{Code: CodeInternal, Message: err.Error()}
		}
		payload = map[string]any{"taxonomy": entries}
	case templateResourceURI:
		tmpl, err := s.eng.TaxonomyTemplate(apiKey(ctx))
		if err != nil {
			return nil, &RPCError{Code: CodeInternal, Message: err.Error()}
		}
		payload = tmpl
	default:
		return nil, &RPCError{Code: CodeNotFound, Message: "resource not found: " + p.URI}
	}
	b, _ := json.Marshal(payload)
	return ResourcesReadResult{Contents: []ResourceContents{{
		URI:      p.URI,
		MimeType: "application/json",
		Text:     string(b),
	}}}, nil
}

func (s *Server) catalogHandler(ctx context.Context, args map[string]any) (ToolResult, error) {
	key := apiKey(ctx)
	prefix, _ := args["prefix"].(string)
	depth := 0
	if d, ok := args["depth"].(float64); ok {
		depth = int(d)
	}
	entries, err := s.eng.Catalog(key, prefix, depth)
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(map[string]any{"taxonomy": entries})
}

func (s *Server) searchHandler(ctx context.Context, args map[string]any) (ToolResult, error) {
	key := apiKey(ctx)
	q, _ := args["q"].(string)
	taxonomy, _ := args["taxonomy"].(string)
	tokenBudget := 0
	if tb, ok := args["token_budget"].(float64); ok {
		tokenBudget = int(tb)
	}
	result, err := s.eng.Search(key, q, taxonomy, tokenBudget)
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(result)
}

func (s *Server) getMemoryHandler(ctx context.Context, args map[string]any) (ToolResult, error) {
	key := apiKey(ctx)
	id, _ := args["id"].(string)
	mem, err := s.eng.GetMemory(key, id)
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(mem)
}

func (s *Server) writeMemoryHandler(ctx context.Context, args map[string]any) (ToolResult, error) {
	key := apiKey(ctx)
	input := engine.WriteInput{
		Title:    strArg(args, "title"),
		Taxonomy: strArg(args, "taxonomy"),
		Content:  strArg(args, "content"),
		Format:   strArg(args, "format"),
		Agent:    strArg(args, "agent"),
	}
	mem, err := s.eng.WriteMemory(key, input)
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(mem)
}

func (s *Server) updateMemoryHandler(ctx context.Context, args map[string]any) (ToolResult, error) {
	key := apiKey(ctx)
	id := strArg(args, "id")
	input := engine.UpdateInput{}
	if v, ok := args["title"].(string); ok {
		input.Title = &v
	}
	if v, ok := args["content"].(string); ok {
		input.Content = &v
	}
	if v, ok := args["taxonomy"].(string); ok {
		input.Taxonomy = &v
	}
	if v, ok := args["format"].(string); ok {
		input.Format = &v
	}
	if v, ok := args["base_version"].(float64); ok {
		n := int(v)
		input.BaseVersion = &n
	}
	input.Agent = strArg(args, "agent")
	mem, err := s.eng.UpdateMemory(key, id, input)
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(mem)
}

func (s *Server) listTeamsHandler(ctx context.Context, _ map[string]any) (ToolResult, error) {
	teams, err := s.eng.MyTeams(apiKey(ctx))
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(map[string]any{"teams": teams})
}

func (s *Server) deletedMemoriesHandler(ctx context.Context, args map[string]any) (ToolResult, error) {
	limit := 0
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}
	tombstones, err := s.eng.DeletedMemories(apiKey(ctx), strArg(args, "taxonomy"), limit)
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(map[string]any{"deleted": tombstones})
}

func (s *Server) memoryHistoryHandler(ctx context.Context, args map[string]any) (ToolResult, error) {
	revs, err := s.eng.MemoryHistory(apiKey(ctx), strArg(args, "id"))
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(map[string]any{"revisions": revs})
}

func (s *Server) restoreRevisionHandler(ctx context.Context, args map[string]any) (ToolResult, error) {
	v, ok := args["version"].(float64)
	if !ok {
		return ToolResult{}, &engine.ActionableError{
			Code:    "missing_fields",
			Message: "version is required and must be a number",
		}
	}
	mem, err := s.eng.RestoreRevision(apiKey(ctx), strArg(args, "id"), int(v))
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(mem)
}

func (s *Server) deleteMemoryHandler(ctx context.Context, args map[string]any) (ToolResult, error) {
	key := apiKey(ctx)
	id := strArg(args, "id")
	force, _ := args["force"].(bool)
	if err := s.eng.DeleteMemory(key, id, force); err != nil {
		return ToolResult{}, err
	}
	return textResult(map[string]string{"deleted": id})
}

func (s *Server) memoryScopeHandler(ctx context.Context, args map[string]any) (ToolResult, error) {
	key := apiKey(ctx)
	taxonomy, _ := args["taxonomy"].(string)
	tokenBudget := 0
	if tb, ok := args["token_budget"].(float64); ok {
		tokenBudget = int(tb)
	}
	result, err := s.eng.ScopeMemories(key, taxonomy, tokenBudget)
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(result)
}

func (s *Server) edgeHealthHandler(ctx context.Context, _ map[string]any) (ToolResult, error) {
	health, err := s.eng.EdgeHealth(apiKey(ctx))
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(health)
}

func (s *Server) graphHandler(ctx context.Context, args map[string]any) (ToolResult, error) {
	center, _ := args["center"].(string)
	taxonomy, _ := args["taxonomy"].(string)
	depth := 0
	if d, ok := args["depth"].(float64); ok {
		depth = int(d)
	}
	g, err := s.eng.Graph(apiKey(ctx), center, depth, taxonomy)
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(g)
}

func (s *Server) suggestLinksHandler(ctx context.Context, args map[string]any) (ToolResult, error) {
	limit := 0
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}
	suggestions, err := s.eng.SuggestLinks(apiKey(ctx), limit)
	if err != nil {
		return ToolResult{}, err
	}
	return textResult(map[string]any{"suggestions": suggestions})
}

func (s *Server) rebuildFTSHandler(ctx context.Context, _ map[string]any) (ToolResult, error) {
	key := apiKey(ctx)
	if err := s.eng.RebuildFTS(key); err != nil {
		return ToolResult{}, err
	}
	return textResult(map[string]string{"status": "ok"})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func textResult(v any) (ToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ToolResult{}, fmt.Errorf("marshal: %w", err)
	}
	return ToolResult{Content: []Content{{Type: "text", Text: string(b)}}}, nil
}

func errResp(id any, code int, msg string, data any) Response {
	return Response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg, Data: data}}
}

func apiKey(ctx context.Context) string {
	v, _ := ctx.Value(keyAPIKey).(string)
	return v
}

func bearerKey(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return h[7:]
	}
	return ""
}

// teamBoundKey is bearerKey plus the team selector: when the request carries an
// X-Rekam-Team header, the bearer is rebound to that team so the whole MCP
// connection acts inside it. A bad bearer or rebind failure falls back to the
// raw key, letting the normal auth path report the error.
func (s *Server) teamBoundKey(r *http.Request) string {
	key := bearerKey(r)
	if team := r.Header.Get("X-Rekam-Team"); team != "" && key != "" {
		if rebound, err := s.eng.BearerForTeam(key, team); err == nil {
			return rebound
		}
	}
	return key
}

func strArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
