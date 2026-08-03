package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sendAndRecv(t *testing.T, in string, root string) []rpcResponse {
	t.Helper()
	var out strings.Builder
	runFilesystemMCPWithIO(strings.NewReader(in), &out, root)
	scanner := bufio.NewScanner(strings.NewReader(out.String()))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var got []rpcResponse
	for scanner.Scan() {
		var r rpcResponse
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatalf("unmarshal response: %v (line=%q)", err, scanner.Text())
		}
		got = append(got, r)
	}
	return got
}

func TestFilesystemMCP_Initialize(t *testing.T) {
	resps := sendAndRecv(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, t.TempDir())
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1", len(resps))
	}
	r := resps[0]
	if r.Error != nil {
		t.Fatalf("error: %+v", r.Error)
	}
	res, _ := r.Result.(map[string]any)
	if res["protocolVersion"] != "2024-11-05" {
		t.Fatalf("protocolVersion = %v", res["protocolVersion"])
	}
	info, _ := res["serverInfo"].(map[string]any)
	if info["name"] != "darvin-filesystem" {
		t.Fatalf("serverInfo.name = %v", info["name"])
	}
}

func TestFilesystemMCP_ToolsList(t *testing.T) {
	resps := sendAndRecv(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, t.TempDir())
	r := resps[0]
	res, _ := r.Result.(map[string]any)
	toolsAny, _ := res["tools"].([]any)
	if len(toolsAny) != 3 {
		t.Fatalf("got %d tools, want 3", len(toolsAny))
	}
	names := map[string]bool{}
	for _, t := range toolsAny {
		tool := t.(map[string]any)
		names[tool["name"].(string)] = true
	}
	for _, want := range []string{"list_directory", "read_file", "write_file"} {
		if !names[want] {
			t.Fatalf("missing tool %q", want)
		}
	}
}

func TestFilesystemMCP_NotificationNoResponse(t *testing.T) {
	resps := sendAndRecv(t, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`, t.TempDir())
	if len(resps) != 0 {
		t.Fatalf("notification produced %d responses, want 0", len(resps))
	}
}

func TestFilesystemMCP_UnknownMethod(t *testing.T) {
	resps := sendAndRecv(t, `{"jsonrpc":"2.0","id":3,"method":"nope","params":{}}`, t.TempDir())
	if resps[0].Error == nil || resps[0].Error.Code != rpcErrMethodNotFound {
		t.Fatalf("expected method not found, got %+v", resps[0].Error)
	}
}

func TestFilesystemMCP_ParseError(t *testing.T) {
	resps := sendAndRecv(t, `not json`, t.TempDir())
	if resps[0].Error == nil || resps[0].Error.Code != rpcErrParse {
		t.Fatalf("expected parse error, got %+v", resps[0].Error)
	}
}

func TestFilesystemMCP_ListReadWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	// list root
	resps := sendAndRecv(t, `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"list_directory","arguments":{}}}`, root)
	if r := resps[0]; r.Error != nil {
		t.Fatalf("list: %+v", r.Error)
	}
	list := resps[0].Result.(map[string]any)
	contents := list["content"].([]any)
	txt := contents[0].(map[string]any)["text"].(string)
	if !strings.Contains(txt, "a.txt") || !strings.Contains(txt, "sub/") {
		t.Fatalf("list_root = %q", txt)
	}

	// read existing
	resps = sendAndRecv(t, `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"a.txt"}}}`, root)
	if r := resps[0]; r.Error != nil {
		t.Fatalf("read: %+v", r.Error)
	}
	contents = resps[0].Result.(map[string]any)["content"].([]any)
	if contents[0].(map[string]any)["text"].(string) != "hello" {
		t.Fatalf("read text = %q", contents[0])
	}

	// write new
	resps = sendAndRecv(t, `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"write_file","arguments":{"path":"sub/b.txt","content":"world"}}}`, root)
	if r := resps[0]; r.Error != nil {
		t.Fatalf("write: %+v", r.Error)
	}
	got, err := os.ReadFile(filepath.Join(root, "sub", "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "world" {
		t.Fatalf("wrote = %q, want world", got)
	}
}

func TestFilesystemMCP_PathTraversalBlocked(t *testing.T) {
	root := t.TempDir()
	resps := sendAndRecv(t, `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"../escape.txt"}}}`, root)
	// path traversal 应该被 resolveWithin 拒绝;tool result isError=true
	// 走 content 文本,而不是 RPC error。
	r := resps[0]
	if r.Error != nil {
		t.Fatalf("expected tool error not rpc error: %+v", r.Error)
	}
	res, _ := r.Result.(map[string]any)
	if res["isError"] != true {
		t.Fatalf("expected isError=true, got %+v", res)
	}
}

func TestFilesystemMCP_ReadMissing(t *testing.T) {
	root := t.TempDir()
	resps := sendAndRecv(t, `{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"nope.txt"}}}`, root)
	r := resps[0]
	if r.Error != nil {
		t.Fatalf("expected tool error not rpc error: %+v", r.Error)
	}
	res, _ := r.Result.(map[string]any)
	if res["isError"] != true {
		t.Fatalf("expected isError=true, got %+v", res)
	}
}

func TestFilesystemMCP_UnknownTool(t *testing.T) {
	resps := sendAndRecv(t, `{"jsonrpc":"2.0","id":22,"method":"tools/call","params":{"name":"delete_everything","arguments":{}}}`, t.TempDir())
	if resps[0].Error == nil || resps[0].Error.Code != rpcErrMethodNotFound {
		t.Fatalf("expected method not found, got %+v", resps[0].Error)
	}
}
