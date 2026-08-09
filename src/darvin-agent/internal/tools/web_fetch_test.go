// Tests for the SSRF guard and HTML extractor behind web_fetch. Network
// requests are not exercised; the pure helpers are covered directly.

package tool

import (
	"strings"
	"testing"
)

func TestSSRFRejectsPrivateIPs(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/x",
		"http://10.0.0.5/x",
		"http://192.168.1.1/x",
		"http://172.16.0.1/x",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/x",
	} {
		if err := ssrfRejectURL(raw); err == nil {
			t.Errorf("ssrfRejectURL should block %s", raw)
		}
	}
}

func TestSSRFRejectsNonHTTP(t *testing.T) {
	if err := ssrfRejectURL("file:///etc/passwd"); err == nil {
		t.Error("non-http scheme should be rejected")
	}
	if err := ssrfRejectURL("ftp://example.com/x"); err == nil {
		t.Error("ftp scheme should be rejected")
	}
}

func TestSSRFAllowsPublicIP(t *testing.T) {
	if err := ssrfRejectURL("http://8.8.8.8/x"); err != nil {
		t.Errorf("public IP should pass SSRF guard: %v", err)
	}
}

func TestHTMLToText(t *testing.T) {
	html := `<html><head><style>.x{color:red}</style></head><body>
<script>alert("hi")</script>
<h1>Title</h1><p>Hello &amp; goodbye</p>
</body></html>`
	got := htmlToText(html)
	if strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Errorf("htmlToText left tags: %q", got)
	}
	if !strings.Contains(got, "Title") || !strings.Contains(got, "Hello & goodbye") {
		t.Errorf("htmlToText lost text: %q", got)
	}
	if strings.Contains(got, "alert") || strings.Contains(got, ".x") {
		t.Errorf("htmlToText kept script/style: %q", got)
	}
}

func TestLooksLikeHTML(t *testing.T) {
	cases := []struct {
		ct, body string
		want     bool
	}{
		{"text/html", "<html>...</html>", true},
		{"", "<!DOCTYPE html><html>", true},
		{"text/plain", "just text", false},
		{"application/json", `{"a":1}`, false},
	}
	for _, c := range cases {
		if got := looksLikeHTML(c.ct, c.body); got != c.want {
			t.Errorf("looksLikeHTML(%q, %q) = %v, want %v", c.ct, c.body, got, c.want)
		}
	}
}
