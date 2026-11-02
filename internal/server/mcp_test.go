package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPToolCatalogue(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range mcpTools {
		if seen[tool.Name] {
			t.Errorf("duplicate tool %s", tool.Name)
		}
		seen[tool.Name] = true
		for _, p := range tool.pathParams() {
			if pathParamHelp[p] == "" {
				t.Errorf("%s: path parameter %s has no description", tool.Name, p)
			}
		}
		if tool.Access == accessRead && tool.Method != "GET" {
			t.Errorf("%s: read-only tool uses %s", tool.Name, tool.Method)
		}
	}
}

func TestMCP(t *testing.T) {
	s := testServer(t)
	s.Handler() // builds the routes, including the API handler MCP calls into
	ctx := context.Background()
	owner, _ := newTestUser(t, s, "mcp", "x")
	org := id(t, s, `insert into organizations (name) values ('t') returning id::text`)
	p := id(t, s, `insert into projects (org_id, name) values ($1,'p') returning id::text`, org)
	s.db.Exec(ctx, `insert into memberships values ($1,$2,'owner')`, org, owner)
	key := func(role string) string {
		token, hash := newToken("orb_key_")
		s.db.Exec(ctx, `insert into api_keys (org_id, name, key_hash, prefix, role, created_by) values ($1,'k',$2,'orb_key_',$3,$4)`, org, hash, role, owner)
		return token
	}
	member, viewer := key("member"), key("viewer")
	rpc := func(token, method string, params any) (int, map[string]any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		r := httptest.NewRequest("POST", "/mcp", bytes.NewReader(b))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.mcp(w, r)
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	call := func(token, tool string, args map[string]any) (bool, string) {
		_, out := rpc(token, "tools/call", map[string]any{"name": tool, "arguments": args})
		res, _ := out["result"].(map[string]any)
		content, _ := res["content"].([]any)
		if len(content) == 0 {
			t.Fatalf("%s: no content in %v", tool, out)
		}
		return res["isError"] == true, content[0].(map[string]any)["text"].(string)
	}

	if code, _ := rpc("", "initialize", nil); code != 401 {
		t.Fatalf("no key: %d", code)
	}
	if _, out := rpc(member, "initialize", map[string]any{"protocolVersion": "2025-06-18"}); out["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize: %v", out)
	}
	if _, out := rpc(member, "tools/list", nil); len(out["result"].(map[string]any)["tools"].([]any)) != len(mcpTools) {
		t.Fatal("tools/list incomplete")
	}

	failed, text := call(member, "create_monitor", map[string]any{"project_id": p, "name": "Site", "type": "http", "target": "https://example.com"})
	if failed {
		t.Fatalf("create_monitor: %s", text)
	}
	var created struct{ ID string }
	json.Unmarshal([]byte(text), &created)
	if failed, text := call(member, "list_monitors", map[string]any{"project_id": p}); failed || !strings.Contains(text, "example.com") {
		t.Fatalf("list_monitors: %s", text)
	}
	if failed, _ := call(viewer, "create_monitor", map[string]any{"project_id": p, "name": "x", "type": "tcp", "target": "a:1"}); !failed {
		t.Fatal("viewer key changed something")
	}
	if failed, text := call(member, "delete_monitor", map[string]any{"project_id": p, "monitor_id": created.ID}); !failed || !strings.Contains(text, "confirm") {
		t.Fatalf("destructive tool ran without confirm: %s", text)
	}
	if failed, text := call(member, "delete_monitor", map[string]any{"project_id": p, "monitor_id": created.ID, "confirm": true}); failed {
		t.Fatalf("confirmed delete: %s", text)
	}
	if failed, _ := call(member, "get_monitor", map[string]any{"project_id": p, "monitor_id": "../../me"}); !failed {
		t.Fatal("path traversal in a path parameter was accepted")
	}
}
