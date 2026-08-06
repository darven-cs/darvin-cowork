package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestServerSpecRoundTrip: the wire contract (main -> Go) is camelCase.
// Marshal a spec, unmarshal it back, and assert Transport survives. This
// is the regression test for the bug where the registry saw an
// empty transport and reported "unsupported transport \"\"".
func TestServerSpecRoundTrip(t *testing.T) {
	in := ServerSpec{
		ID:          "filesystem",
		Name:        "Filesystem",
		Enabled:     true,
		Transport:   TransportStdio,
		Command:     "/usr/bin/darvin-agent",
		Args:        []string{"mcp-filesystem"},
		IsBuiltIn:   true,
		GitHubURL:   "https://example.com/fs",
		RegistryID:  "fs",
		Description: "local fs",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out ServerSpec
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Transport != TransportStdio {
		t.Fatalf("Transport = %q, want %q (raw %s)", out.Transport, TransportStdio, b)
	}
	if out.ID != in.ID || out.Name != in.Name || !out.Enabled || !out.IsBuiltIn {
		t.Fatalf("scalar fields not preserved: %+v", out)
	}
	if len(out.Args) != 1 || out.Args[0] != "mcp-filesystem" {
		t.Fatalf("Args = %v, want [mcp-filesystem]", out.Args)
	}
	if out.RegistryID != "fs" || out.GitHubURL != in.GitHubURL || out.Description != in.Description {
		t.Fatalf("extended fields not preserved: %+v", out)
	}
}

// TestServerSpecDecodeCamelCase: simulate the main-side IPC payload that
// ships transportType / isBuiltIn / registryId in camelCase. Without JSON
// tags on ServerSpec the transport field silently drops to "".
func TestServerSpecDecodeCamelCase(t *testing.T) {
	payload := `{"id":"fs","name":"Filesystem","enabled":true,"transportType":"stdio","isBuiltIn":true,"registryId":"fs"}`
	var spec ServerSpec
	if err := json.Unmarshal([]byte(payload), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if spec.Transport != TransportStdio {
		t.Fatalf("Transport = %q, want %q", spec.Transport, TransportStdio)
	}
	if !spec.IsBuiltIn {
		t.Fatal("IsBuiltIn = false, want true")
	}
	if spec.RegistryID != "fs" {
		t.Fatalf("RegistryID = %q, want fs", spec.RegistryID)
	}
}

// TestServerSpecMarshalCamelCase: the Go side never marshals ServerSpec
// back over the wire today (it converts to McpServerWire), but a
// regression that removes tags should still fail loudly here rather than
// silently producing PascalCase keys.
func TestServerSpecMarshalCamelCase(t *testing.T) {
	b, err := json.Marshal(ServerSpec{ID: "x", Transport: TransportHTTP})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw := string(b)
	for _, want := range []string{`"id":"x"`, `"transportType":"http"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("marshal output missing %s: %s", want, raw)
		}
	}
}

func TestRedactCredentials_GitHubPAT(t *testing.T) {
	input := `npm install --registry=https://ghp.xyz/repo ghp_abcdefghijklmnopqrstuvwxyz1234567890abcdef`
	got := RedactCredentials(input)
	if got == input {
		t.Fatal("GitHub PAT not redacted")
	}
	if strings.Contains(got, "ghp_abc") {
		t.Fatalf("redacted string still contains token prefix: %s", got)
	}
	if !strings.Contains(got, "[GITHUB_TOKEN]") {
		t.Fatalf("redacted string missing placeholder: %s", got)
	}
}

func TestRedactCredentials_MultipleTokens(t *testing.T) {
	input := `ghp_abcdefghijklmnopqrstuvwxyz1234567890abcdef sk-hello_world_1234567890abcdef`
	got := RedactCredentials(input)
	if strings.Contains(got, "ghp_") || strings.Contains(got, "sk-hel") {
		t.Fatalf("tokens not fully redacted: %s", got)
	}
}

func TestRedactCredentials_NoToken(t *testing.T) {
	input := `npm install --legacy-peer-deps`
	got := RedactCredentials(input)
	if got != input {
		t.Fatalf("clean string was modified: %s", got)
	}
}


func TestStartupFailure_Fields(t *testing.T) {
	sf := StartupFailure{
		Stage:   "spawn",
		Stderr:  "command not found: npx",
		Err:     "exec: not found",
	}
	if sf.Stage != "spawn" {
		t.Fatalf("Stage = %q, want spawn", sf.Stage)
	}
	if sf.Stderr != "command not found: npx" {
		t.Fatalf("Stderr = %q", sf.Stderr)
	}
}

func TestRedactCredentials_BearerToken(t *testing.T) {
	input := `Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U`
	got := RedactCredentials(input)
	if strings.Contains(got, "eyJ") {
		t.Fatalf("JWT not redacted: %s", got)
	}
}
