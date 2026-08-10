// Tests for connection auth diagnosis.

package mcp

import "testing"

func TestDiagnoseAuth(t *testing.T) {
	cases := []struct {
		name           string
		transport      TransportType
		connected      bool
		errText        string
		url            string
		hasCreds       bool
		wantStatus     AuthStatus
		wantSuggestion bool
	}{
		{"connected no auth", TransportHTTP, true, "", "https://x.com/mcp", false, AuthNone, false},
		{"401 over http https no creds → required", TransportHTTP, false, "401 Unauthorized", "https://x.com/mcp", false, AuthRequired, true},
		{"401 over http with creds → none + suggestion", TransportHTTP, false, "401 Unauthorized", "https://x.com/mcp", true, AuthNone, true},
		{"401 over stdio → none + suggestion", TransportStdio, false, "401 Unauthorized", "", false, AuthNone, true},
		{"401 over http loopback → required", TransportHTTP, false, "unauthorized", "http://localhost:3001/mcp", false, AuthRequired, true},
		{"403 over http non-loopback → none", TransportHTTP, false, "403 Forbidden", "http://10.0.0.5/mcp", false, AuthNone, true},
		{"not connected yet → possible", TransportHTTP, false, "", "https://x.com/mcp", false, AuthPossible, true},
		{"connection refused → possible", TransportHTTP, false, "connection refused", "https://x.com/mcp", false, AuthPossible, true},
	}
	for _, c := range cases {
		got := DiagnoseAuth(c.transport, c.connected, c.errText, c.url, c.hasCreds)
		if got.Status != c.wantStatus {
			t.Errorf("%s: status = %q, want %q", c.name, got.Status, c.wantStatus)
		}
		if c.wantSuggestion && got.Suggestion == "" {
			t.Errorf("%s: expected a suggestion, got empty", c.name)
		}
		if !c.wantSuggestion && got.Suggestion != "" {
			t.Errorf("%s: expected no suggestion, got %q", c.name, got.Suggestion)
		}
	}
}

func TestHasExplicitCredentials(t *testing.T) {
	if HasExplicitCredentials(ServerSpec{Env: map[string]string{"HOST": "x"}}) {
		t.Error("HOST should not count as a credential")
	}
	if !HasExplicitCredentials(ServerSpec{Env: map[string]string{"API_KEY": "x"}}) {
		t.Error("API_KEY should count as a credential")
	}
	if !HasExplicitCredentials(ServerSpec{Headers: map[string]string{"Authorization": "Bearer x"}}) {
		t.Error("Authorization header should count as a credential")
	}
	if HasExplicitCredentials(ServerSpec{}) {
		t.Error("empty spec should not count as having credentials")
	}
}
