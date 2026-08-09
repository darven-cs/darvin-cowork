// Implements the web_fetch tool: an SSRF-guarded HTTP GET that reduces
// HTML pages to readable text.

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"darvin-cowork/backend/internal/llm"
)

// webFetchMaxBytes caps a single response body (internal constant, not
// exposed as a parameter; the executor's per-tool timeout bounds the call).
const webFetchMaxBytes = 1 << 20

// blockedNetworks are private / reserved CIDR blocks a web_fetch target must
// never resolve to. This is the SSRF guard: it is a hard constraint with no
// operator switch.
var blockedNetworks = compileBlockedNetworks()

func compileBlockedNetworks() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
		"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
		"192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
		"224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
		"::1/128", "fc00::/7", "fe80::/10", "ff00::/8",
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// ssrfRejectURL validates the target scheme and host: only http/https, and
// the host must not resolve to any blocked (private / reserved) address.
func ssrfRejectURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("only http:// and https:// URLs are supported")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("URL has no host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("refusing to fetch private/reserved address %s", ip)
		}
	}
	return nil
}

// isBlockedIP reports whether ip falls in any blocked network.
func isBlockedIP(ip net.IP) bool {
	for _, n := range blockedNetworks {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

var (
	// RE2 has no backreferences, so script and style blocks are matched
	// with two separate non-greedy expressions.
	reScript = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	reStyle  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace  = regexp.MustCompile(`[ \t\r\n]+`)
)

// htmlToText strips script/style blocks and tags, unescapes entities, and
// collapses whitespace. A minimal stdlib extractor by design; swap in
// golang.org/x/net/html if extraction quality ever becomes a requirement.
func htmlToText(s string) string {
	s = reScript.ReplaceAllString(s, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = reTag.ReplaceAllString(s, " ")
	s = stdhtml.UnescapeString(s)
	s = reSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// looksLikeHTML reports whether the body should be reduced to text: the
// Content-Type says HTML, or the head smells like an HTML document.
func looksLikeHTML(contentType, body string) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml+xml") {
		return true
	}
	head := strings.ToLower(strings.TrimSpace(body))
	if len(head) > 512 {
		head = head[:512]
	}
	return strings.Contains(head, "<html") || strings.HasPrefix(head, "<!doctype html")
}

// webFetchTool fetches a URL over HTTP(S) and returns its text content.
type webFetchTool struct{}

func (t *webFetchTool) Name() string { return "web_fetch" }
func (t *webFetchTool) Description() string {
	return "Fetch a URL over HTTPS/HTTP and return its text content. HTML pages are reduced to readable text (scripts, styles, tags stripped, whitespace collapsed); JSON / plain text / markdown bodies come back verbatim. Use it to read documentation pages, API responses, or source files the local filesystem can't reach."
}
func (t *webFetchTool) Parameters() json.RawMessage {
	return MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"url": {Type: "string", Description: "Absolute URL beginning with http:// or https://."},
		},
		Required:             []string{"url"},
		AdditionalProperties: ptrBool(false),
	})
}

func (t *webFetchTool) Execute(ctx context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	rawURL, _ := args["url"].(string)
	if err := ssrfRejectURL(rawURL); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	req.Header.Set("User-Agent", "darvin-agent-web-fetch/1.0")
	req.Header.Set("Accept", "text/html,text/plain,text/markdown,application/json,*/*;q=0.5")
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := ssrfRejectURL(req.URL.String()); err != nil {
				return err
			}
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{IsError: true, Content: fmt.Sprintf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBytes+1))
	if err != nil {
		return Result{IsError: true, Content: "read body: " + err.Error()}
	}
	truncated := len(body) > webFetchMaxBytes
	if truncated {
		body = body[:webFetchMaxBytes]
	}
	text := string(body)
	if looksLikeHTML(resp.Header.Get("Content-Type"), text) {
		text = htmlToText(text)
	}
	if truncated {
		text += fmt.Sprintf("\n[body truncated at %d bytes]", webFetchMaxBytes)
	}
	if strings.TrimSpace(text) == "" {
		return Result{Content: "(empty body)"}
	}
	return Result{Content: text}
}

func init() {
	RegisterBuiltinFactory("web_fetch", func(cfg BuiltinConfig) (Tool, error) {
		return &webFetchTool{}, nil
	})
}
