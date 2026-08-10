// Tests for credential redaction.

package mcp

import (
	"testing"
)

func TestRedactString(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"plain message", "plain message"},
		{"connect: status 401 unauthorized", "connect: status 401 unauthorized"},
		{"Authorization: Bearer sk-ant-abc123def", "Authorization: Bearer ***"},
		{"token: abcdef123456", "token: ***"},
		{"api_key=ghp_abcdef123456", "api_key=***"},
		{"https://user:supersecret@example.com/mcp", "https://***:***@example.com/mcp"},
		{"failed: npm install github_token=abc123 failed", "failed: npm install github_token=*** failed"},
	}
	for _, c := range cases {
		if got := RedactString(c.in); got != c.want {
			t.Errorf("RedactString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsSecretKey(t *testing.T) {
	secret := []string{"api_key", "API_KEY", "Authorization", "x-api-token", "password", "client_secret", "bearer"}
	for _, k := range secret {
		if !IsSecretKey(k) {
			t.Errorf("IsSecretKey(%q) = false, want true", k)
		}
	}
	plain := []string{"PATH", "HOST", "PORT", "DEBUG", "NODE_ENV"}
	for _, k := range plain {
		if IsSecretKey(k) {
			t.Errorf("IsSecretKey(%q) = true, want false", k)
		}
	}
}

func TestRedactMap(t *testing.T) {
	m := map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-secret",
		"HOST":              "localhost",
		"Authorization":     "Bearer xyz",
	}
	out := RedactMap(m)
	if out["ANTHROPIC_API_KEY"] != "***" {
		t.Errorf("API key not masked: %q", out["ANTHROPIC_API_KEY"])
	}
	if out["Authorization"] != "***" {
		t.Errorf("Authorization not masked: %q", out["Authorization"])
	}
	if out["HOST"] != "localhost" {
		t.Errorf("HOST wrongly masked: %q", out["HOST"])
	}
	// Original map must not be mutated.
	if m["ANTHROPIC_API_KEY"] != "sk-ant-secret" {
		t.Error("RedactMap mutated the input map")
	}
}

func TestRedactSpec(t *testing.T) {
	s := ServerSpec{
		ID:      "gh",
		Env:     map[string]string{"GITHUB_TOKEN": "ghp_secret", "PORT": "8080"},
		Headers: map[string]string{"Authorization": "Bearer tok"},
		URL:     "https://user:pass@example.com/mcp",
	}
	out := RedactSpec(s)
	if out.Env["GITHUB_TOKEN"] != "***" {
		t.Errorf("spec env secret not masked: %q", out.Env["GITHUB_TOKEN"])
	}
	if out.Env["PORT"] != "8080" {
		t.Errorf("spec env PORT wrongly masked: %q", out.Env["PORT"])
	}
	if out.Headers["Authorization"] != "***" {
		t.Errorf("spec header not masked: %q", out.Headers["Authorization"])
	}
	if out.URL != "https://***:***@example.com/mcp" {
		t.Errorf("spec URL userinfo not masked: %q", out.URL)
	}
	// Original spec untouched.
	if s.Env["GITHUB_TOKEN"] != "ghp_secret" {
		t.Error("RedactSpec mutated the input spec")
	}
}

func TestRedactResolution(t *testing.T) {
	r := LaunchResolution{
		ServerID:      "gh",
		Error:         "npm install failed with token sk-ant-xyz",
		FailureStderr: "Authorization: Bearer abcdef",
		Env:           map[string]string{"GITHUB_TOKEN": "ghp_secret"},
		Args:          []string{"--token", "abc123", "--registry=https://user:pw@npm.example.com", "--port", "8080"},
	}
	out := RedactResolution(r)
	if out.Error != "npm install failed with token ***" {
		t.Errorf("resolution error not redacted: %q", out.Error)
	}
	if out.FailureStderr != "Authorization: Bearer ***" {
		t.Errorf("resolution stderr not redacted: %q", out.FailureStderr)
	}
	if out.Env["GITHUB_TOKEN"] != "***" {
		t.Errorf("resolution env not masked: %q", out.Env["GITHUB_TOKEN"])
	}
	if out.Args[0] != "--token" || out.Args[1] != "***" {
		t.Errorf("resolution secret flag value not masked: %v", out.Args[:2])
	}
	if out.Args[2] != "--registry=https://***:***@npm.example.com" {
		t.Errorf("resolution userinfo not masked: %v", out.Args[2])
	}
	if out.Args[3] != "--port" || out.Args[4] != "8080" {
		t.Errorf("resolution non-secret args wrongly masked: %v", out.Args[3:])
	}
}
