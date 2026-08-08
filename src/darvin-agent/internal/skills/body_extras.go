package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const skillFile = "SKILL.md"

// loadBodyWithReferences appends a directory-layout skill's sibling
// references/*.md files to its body in deterministic order, so depth
// material is available without on-demand resolution. Flat skills have no
// references dir and are returned unchanged.
func loadBodyWithReferences(skillPath, body string) string {
	if filepath.Base(skillPath) != skillFile {
		return body
	}
	refsDir := filepath.Join(filepath.Dir(skillPath), "references")
	entries, err := os.ReadDir(refsDir)
	if err != nil {
		return body
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return body
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(body)
	for _, n := range names {
		content, err := os.ReadFile(filepath.Join(refsDir, n))
		if err != nil {
			continue
		}
		trimmed := strings.TrimSpace(string(content))
		if trimmed == "" {
			continue
		}
		slug := strings.TrimSuffix(n, filepath.Ext(n))
		b.WriteString("\n\n## Reference: " + slug + "\n\n" + trimmed)
	}
	return b.String()
}

// loadBodyWithScripts lists a directory-layout skill's sibling scripts/*
// files in the body so the model can bash-run them with the exact path.
// Hidden files and unsupported extensions are filtered out.
func loadBodyWithScripts(skillPath, body string) string {
	if filepath.Base(skillPath) != skillFile {
		return body
	}
	scriptsDir := filepath.Join(filepath.Dir(skillPath), "scripts")
	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		return body
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !isScriptExt(filepath.Ext(e.Name())) {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return body
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(body)
	b.WriteString("\n\n## Scripts\n\nRun a listed script with bash using the exact path shown below; quote the path if it contains spaces.\n\n")
	for _, n := range names {
		b.WriteString("- `" + filepath.Join(scriptsDir, n) + "`\n")
	}
	return b.String()
}

func isScriptExt(ext string) bool {
	switch strings.ToLower(ext) {
	case "", ".sh", ".py", ".js", ".ts", ".rb", ".pl", ".php", ".ps1":
		return true
	default:
		return false
	}
}
