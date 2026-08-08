// Tests for JSON-RPC frame parsing and response shaping.

package gateway

import (
	"encoding/json"
	"testing"
)

func TestParseFrameSingle(t *testing.T) {
	reqs, batch, err := parseFrame([]byte(`{"jsonrpc":"2.0","id":"1","method":"agent.prompt","params":{"content":"hi"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if batch {
		t.Fatalf("expected single, got batch")
	}
	if len(reqs) != 1 || reqs[0].Method != "agent.prompt" {
		t.Fatalf("unexpected: %+v", reqs)
	}
	if string(reqs[0].ID) != `"1"` {
		t.Fatalf("id roundtrip: got %s", reqs[0].ID)
	}
}

func TestParseFrameBatch(t *testing.T) {
	reqs, batch, err := parseFrame([]byte(`[{"jsonrpc":"2.0","id":"1","method":"a"},{"jsonrpc":"2.0","id":"2","method":"b"}]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !batch {
		t.Fatalf("expected batch")
	}
	if len(reqs) != 2 || reqs[0].Method != "a" || reqs[1].Method != "b" {
		t.Fatalf("unexpected: %+v", reqs)
	}
}

func TestParseFrameInvalid(t *testing.T) {
	_, _, err := parseFrame([]byte(`{not-json`))
	if err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestSuccessRespOmitsError(t *testing.T) {
	r := successResp(json.RawMessage(`"1"`), map[string]any{"ok": true})
	b, _ := json.Marshal(r)
	if got := string(b); got != `{"jsonrpc":"2.0","id":"1","result":{"ok":true}}` {
		t.Fatalf("got %s", got)
	}
}

func TestErrorRespOmitsResult(t *testing.T) {
	r := errorResp(json.RawMessage(`"3"`), CodeMethodNotFound, "Method not found: x", nil)
	b, _ := json.Marshal(r)
	if got := string(b); got != `{"jsonrpc":"2.0","id":"3","error":{"code":-32601,"message":"Method not found: x"}}` {
		t.Fatalf("got %s", got)
	}
}

func TestNotificationHasNoID(t *testing.T) {
	n := newNotification("agent.event", map[string]any{"type": "text_delta"})
	b, _ := json.Marshal(n)
	if got := string(b); got != `{"jsonrpc":"2.0","method":"agent.event","params":{"type":"text_delta"}}` {
		t.Fatalf("got %s", got)
	}
}
