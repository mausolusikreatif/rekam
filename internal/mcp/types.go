// Package mcp implements the Model Context Protocol (MCP) spec v2024-11-05.
// Transport: JSON-RPC 2.0 over stdio (line-delimited) and HTTP SSE.
package mcp

// ── JSON-RPC 2.0 wire types ──────────────────────────────────────────────────

// Message is the raw inbound JSON-RPC frame.
type Message struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"` // number | string | null; absent = notification
	Method  string `json:"method,omitempty"`
	// Params is kept as raw JSON so each handler can decode its own shape.
	Params  rawJSON `json:"params,omitempty"`
}

// Response is the outbound JSON-RPC frame.
type Response struct {
	JSONRPC string   `json:"jsonrpc"`
	ID      any      `json:"id,omitempty"`
	Result  any      `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

// RPCError is the JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC error codes.
const (
	CodeParse     = -32700
	CodeInvalid   = -32600
	CodeNotFound  = -32601
	CodeParams    = -32602
	CodeInternal  = -32603
)

// ── MCP protocol types ────────────────────────────────────────────────────────

// InitializeResult is returned from the initialize handshake.
type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
	Instructions    string       `json:"instructions,omitempty"`
}

type Capabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Tool describes one tool in the tools/list response.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
	Annotations *Annotations `json:"annotations,omitempty"`
}

// InputSchema is a JSON Schema (object subset) for the tool's arguments.
type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

// Property is a single JSON Schema property.
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// Annotations conveys hints to the MCP client.
// DestructiveHint=true causes Claude to surface an allow/deny confirmation.
type Annotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint,omitempty"`
	DestructiveHint bool `json:"destructiveHint,omitempty"`
}

// ToolsListResult is the result of tools/list.
type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

// ToolCallParams is the params block for tools/call.
type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ToolResult is the result of tools/call.
type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Content is a single content item in a tool result.
type Content struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// ── MCP resources ─────────────────────────────────────────────────────────────

// Resource describes one resource in the resources/list response.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ResourcesListResult is the result of resources/list.
type ResourcesListResult struct {
	Resources []Resource `json:"resources"`
}

// ResourcesReadParams is the params block for resources/read.
type ResourcesReadParams struct {
	URI string `json:"uri"`
}

// ResourceContents is one content item in a resources/read response.
type ResourceContents struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// ResourcesReadResult is the result of resources/read.
type ResourcesReadResult struct {
	Contents []ResourceContents `json:"contents"`
}

// rawJSON lets us defer decoding of the Params field.
type rawJSON []byte

func (r rawJSON) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	return []byte(r), nil
}

func (r *rawJSON) UnmarshalJSON(data []byte) error {
	*r = rawJSON(data)
	return nil
}
