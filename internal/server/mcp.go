// MCP server for AI apps (Claude, ChatGPT, Cursor, Codex, Copilot): JSON-RPC over Streamable HTTP at /mcp.
// Each tool maps to an API route (mcp_tools.go) and is executed through the normal API handler with the
// caller's API key, so role checks, project scoping, 2FA rules and the audit log all apply unchanged.
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "this MCP server answers POST requests only (no SSE stream)", http.StatusMethodNotAllowed)
		return
	}
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Nolyx Orbit", error="invalid_token"`)
		writeJSON(w, 401, map[string]string{"error": "send an Orbit API key as Authorization: Bearer orb_key_…"})
		return
	}
	if _, err := s.keyPrincipal(r.Context(), bearer); err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Nolyx Orbit", error="invalid_token"`)
		writeJSON(w, 401, map[string]string{"error": "invalid or expired API key"})
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || req.JSONRPC != "2.0" {
		writeJSON(w, 400, map[string]any{"jsonrpc": "2.0", "id": nil, "error": rpcError{-32700, "parse error"}})
		return
	}
	if req.ID == nil { // a notification, such as notifications/initialized
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, rerr := s.mcpDispatch(r, req)
	out := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if rerr != nil {
		out["error"] = rerr
	} else {
		out["result"] = result
	}
	writeJSON(w, 200, out)
}

func (s *Server) mcpDispatch(r *http.Request, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		version := mcpVersions[0]
		if oneOf(p.ProtocolVersion, mcpVersions) {
			version = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "nolyx-orbit", "title": "Nolyx Orbit", "version": "1"},
			"instructions": "Tools act on the user's self-hosted Nolyx Orbit infrastructure. Start with list_projects to find project_id. " +
				"Tools marked destructive require confirm=true: ask the user and get an explicit yes before setting it.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		tools := make([]map[string]any, 0, len(mcpTools))
		for _, t := range mcpTools {
			tools = append(tools, t.describe())
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		t, ok := mcpToolByName[p.Name]
		if !ok {
			return nil, &rpcError{-32602, "unknown tool " + p.Name}
		}
		return s.mcpCall(r, t, p.Arguments), nil
	}
	return nil, &rpcError{-32601, "method not found: " + req.Method}
}

// mcpCall runs a tool through the API handler as the caller and returns the response as tool content.
func (s *Server) mcpCall(r *http.Request, t mcpTool, args map[string]any) map[string]any {
	fail := func(msg string) map[string]any {
		return map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": msg}}}
	}
	if args == nil {
		args = map[string]any{}
	}
	if t.Access == accessDestructive && args["confirm"] != true {
		return fail("This action can't be undone. Ask the user to approve it, then call again with confirm=true.")
	}
	delete(args, "confirm")
	path := t.Path
	for _, name := range t.pathParams() {
		v, ok := args[name].(string)
		if !ok || v == "" || strings.ContainsAny(v, "/?#") {
			return fail(name + " is required")
		}
		path = strings.Replace(path, "{"+name+"}", url.PathEscape(v), 1)
		delete(args, name)
	}
	for _, p := range t.Params {
		if v, ok := args[p.Name]; ok && p.As != "" {
			delete(args, p.Name)
			args[p.As] = v
		}
	}
	var body *bytes.Reader
	if t.Method == "GET" || t.Method == "DELETE" {
		q := url.Values{}
		for k, v := range args {
			b, _ := json.Marshal(v)
			q.Set(k, strings.Trim(string(b), `"`))
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		body = bytes.NewReader(nil)
	} else {
		body = bytes.NewReader(mustJSON(args))
	}
	req, _ := http.NewRequestWithContext(r.Context(), t.Method, "/api/v1"+path, body)
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = r.RemoteAddr
	req.Header.Set("X-Forwarded-For", r.Header.Get("X-Forwarded-For"))
	rec := &recorder{header: http.Header{}, code: 200}
	s.apiHandler.ServeHTTP(rec, req)
	text := rec.body.String()
	if text == "" {
		text = http.StatusText(rec.code)
	}
	if rec.code >= 400 {
		return fail(text)
	}
	return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}
}

// mcpToolList is for the AI apps settings page.
func (s *Server) mcpToolList(w http.ResponseWriter, r *http.Request) error {
	out := make([]map[string]string, 0, len(mcpTools))
	for _, t := range mcpTools {
		out = append(out, map[string]string{"name": t.Name, "description": t.Description, "access": t.Access})
	}
	writeJSON(w, 200, out)
	return nil
}

// recorder captures an in-process API response.
type recorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *recorder) WriteHeader(code int)        { r.code = code }
