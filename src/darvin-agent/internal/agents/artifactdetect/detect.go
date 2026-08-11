// Package artifactdetect provides pure regex / extension detection that
// turns assistant text into artifact descriptions for the renderer's
// artifact panel. Kept dependency-free so both the agent root package
// (turn-end hook) and the executor (write_file capture) can use it.
package artifactdetect

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

// Detected is a single artifact candidate extracted from text.
type Detected struct {
	Kind     string
	Name     string
	Content  string
	FilePath string
	URL      string
}

// extKind maps a lowercase file extension to an artifact kind, aligned
// with the renderer's DarvinArtifactKind union.
var extKind = map[string]string{
	"html": "html", "htm": "html", "svg": "svg",
	"png": "image", "jpg": "image", "jpeg": "image", "gif": "image",
	"webp": "image", "bmp": "image", "avif": "image",
	"mp4": "video", "webm": "video", "mov": "video",
	"mermaid": "mermaid",
	"md": "markdown", "markdown": "markdown",
	"txt": "text", "log": "text", "csv": "text", "tsv": "text", "json": "text",
	"docx": "document", "xlsx": "document", "xls": "document",
	"pptx": "document", "pdf": "document",
	"py": "code", "js": "code", "ts": "code", "go": "code", "rs": "code",
	"c": "code", "cpp": "code", "h": "code", "java": "code", "kt": "code",
	"swift": "code", "rb": "code", "php": "code", "sh": "code", "css": "code",
	"scss": "code", "yml": "code", "yaml": "code", "toml": "code", "xml": "code",
	"vue": "code", "tsx": "code", "jsx": "code", "ipynb": "code", "sql": "code",
}

// KindForPath returns the artifact kind for a file path by extension.
func KindForPath(p string) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(p), "."))
	k, ok := extKind[ext]
	return k, ok
}

// ArtifactID derives a stable id from a file path or URL so the same
// source detected from different angles (turn-end scan vs write_file
// capture) collapses to one artifact in the renderer.
func ArtifactID(key string) string {
	sum := sha1.Sum([]byte(key))
	return "artifact-" + hex.EncodeToString(sum[:6])
}

var (
	localServiceRe = regexp.MustCompile(`\bhttps?://(?:localhost|127\.\d{1,3}\.\d{1,3}\.\d{1,3}|0\.0\.0\.0|\[::1\])(?::\d{1,5})?(?:[/?#][^\s)]*)?`)
	remoteImageRe  = regexp.MustCompile(`\bhttps?://[^\s)]+\.(?:png|jpe?g|gif|webp|bmp|avif)(?:\?[^\s)]*)?`)
	fileLinkRe     = regexp.MustCompile(`\[[^\]]*\]\(file://([^)]+)\)`)
	barePathRe     = regexp.MustCompile(`(?:^|[\s(\[])(/?(?:[\w.-]+/)+[\w.-]+\.[A-Za-z0-9]+)`)
	mediaTokenRe   = regexp.MustCompile(`(?m)\bMEDIA:\s*(\S+)\s*$`)
)

// Scan extracts local-service URLs, remote image URLs, file:// links,
// bare paths and MEDIA tokens from assistant text. Workdir, when non-empty,
// is used to relativise absolute file paths inside the workspace.
func Scan(text, workdir string) []Detected {
	var out []Detected
	seen := map[string]bool{}
	add := func(d Detected) {
		key := d.Kind + "|" + d.URL + "|" + d.FilePath
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, d)
	}

	for _, m := range localServiceRe.FindAllString(text, -1) {
		m = trimTrailing(m)
		add(Detected{Kind: "local-service", Content: m, URL: m, Name: m})
	}
	for _, m := range remoteImageRe.FindAllString(text, -1) {
		m = trimTrailing(m)
		add(Detected{Kind: "image", Content: m, URL: m, Name: m})
	}
	for _, sm := range fileLinkRe.FindAllStringSubmatch(text, -1) {
		raw := stripQueryHash(sm[1])
		if kind, ok := KindForPath(raw); ok {
			add(Detected{Kind: kind, Content: raw, FilePath: normalize(raw, workdir), Name: filepath.Base(raw)})
		}
	}
	for _, sm := range barePathRe.FindAllStringSubmatch(text, -1) {
		raw := stripQueryHash(sm[1])
		if kind, ok := KindForPath(raw); ok {
			add(Detected{Kind: kind, Content: raw, FilePath: normalize(raw, workdir), Name: filepath.Base(raw)})
		}
	}
	for _, sm := range mediaTokenRe.FindAllStringSubmatch(text, -1) {
		raw := stripQueryHash(strings.TrimSpace(sm[1]))
		if kind, ok := KindForPath(raw); ok {
			add(Detected{Kind: kind, Content: raw, FilePath: normalize(raw, workdir), Name: filepath.Base(raw)})
		}
	}
	return out
}

func trimTrailing(s string) string {
	return strings.TrimRight(s, "),.;。，")
}

func stripQueryHash(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		return p[:i]
	}
	return p
}

// normalize relativises an absolute path inside workdir; everything else
// is returned unchanged (relative paths, paths outside the workspace).
func normalize(path, workdir string) string {
	if workdir == "" {
		return path
	}
	abs := filepath.Clean(path)
	w := filepath.Clean(workdir)
	if rel, err := filepath.Rel(w, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
