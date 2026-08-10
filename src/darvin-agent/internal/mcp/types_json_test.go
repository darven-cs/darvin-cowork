// Tests for the server spec JSON wire contract.

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

// TestServerSpecTrustLevelRoundTrip: trustLevel crosses the wire as a
// plain string; an absent field decodes to "" and EffectiveTrustLevel
// resolves it to "ask".
func TestServerSpecTrustLevelRoundTrip(t *testing.T) {
	payload := `{"id":"gh","trustLevel":"trusted"}`
	var spec ServerSpec
	if err := json.Unmarshal([]byte(payload), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if spec.EffectiveTrustLevel() != TrustTrusted {
		t.Fatalf("EffectiveTrustLevel = %q, want trusted", spec.EffectiveTrustLevel())
	}
	var empty ServerSpec
	if empty.EffectiveTrustLevel() != TrustAsk {
		t.Fatalf("default EffectiveTrustLevel = %q, want ask", empty.EffectiveTrustLevel())
	}
}

// TestToolAnnotationRoundTrip: the MCP tool annotation block decodes from
// the wire shape servers emit (camelCase hint booleans). A nil DestructiveHint
// distinguishes "not declared" from an explicit false.
func TestToolAnnotationRoundTrip(t *testing.T) {
	payload := `{"name":"x","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true,"destructiveHint":false}}`
	var td ToolDescriptor
	if err := json.Unmarshal([]byte(payload), &td); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if td.Annotations == nil {
		t.Fatal("Annotations = nil, want parsed block")
	}
	if td.Annotations.ReadOnlyHint == nil || !*td.Annotations.ReadOnlyHint {
		t.Fatal("ReadOnlyHint should be true")
	}
	if td.Annotations.DestructiveHint == nil || *td.Annotations.DestructiveHint {
		t.Fatal("DestructiveHint should be explicitly false")
	}
	if td.Annotations.OpenWorldHint != nil {
		t.Fatal("OpenWorldHint should stay nil when not declared")
	}
}
