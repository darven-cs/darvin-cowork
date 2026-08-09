// Loads skill entries from bundled, project, and global skill sources.

package skills

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxSkillFileSize = 256 * 1024
const defaultScanMaxDepth = 3
const absoluteScanMaxDepth = 5

// BundledSource loads skills embedded into the binary via //go:embed.
// The plumbing is retained for future built-in skills; current main.go
// does not wire one.
type BundledSource struct {
	FS  embed.FS
	Dir string
}

func (s *BundledSource) LoadAll(ctx context.Context) ([]*SkillEntry, error) {
	return loadEmbeddedSkills(ctx, s.FS, s.Dir)
}

// UserSource scans a directory tree for skills. Two layouts are recognised:
//   - directory layout: <name>/SKILL.md (canonical)
//   - flat layout:       <name>.md with skill frontmatter (Claude-compatible,
//     only when RequireFlatMarker is true)
//
// Sub-directories whose name starts with a dot, or matches the well-known
// noise dirs (assets / node_modules / references / scripts), are not
// recursed into. Symlinks are followed for reads and deduplicated via
// EvalSymlinks to keep cycles bounded.
type UserSource struct {
	RootDir string
	Source  SkillSource
	// RequireFlatMarker gates whether flat <name>.md files are eligible.
	// Set true only on roots known to mix skill and non-skill markdown
	// (e.g. .claude/skills); otherwise false.
	RequireFlatMarker bool
	// MaxDepth is clamped to [1, absoluteScanMaxDepth]; zero falls back
	// to defaultScanMaxDepth.
	MaxDepth int
}

func (s *UserSource) LoadAll(ctx context.Context) ([]*SkillEntry, error) {
	if s.RootDir == "" {
		return nil, nil
	}
	if _, err := os.Stat(s.RootDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	source := s.Source
	if source == "" {
		source = SkillSourceGlobal
	}
	maxDepth := s.MaxDepth
	if maxDepth == 0 {
		maxDepth = defaultScanMaxDepth
	} else if maxDepth < 1 {
		maxDepth = 1
	} else if maxDepth > absoluteScanMaxDepth {
		maxDepth = absoluteScanMaxDepth
	}
	return scanDir(ctx, s.RootDir, source, s.RequireFlatMarker, 1, map[string]bool{}, maxDepth)
}

// scanDir walks one root directory and yields SkillEntry per recognised
// layout. It recurses into subdirectories whose names do not match the
// skip list, up to maxDepth levels deep.
func scanDir(ctx context.Context, dir string, source SkillSource, requireFlatMarker bool, depth int, seen map[string]bool, maxDepth int) ([]*SkillEntry, error) {
	key := filepath.Clean(dir)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		key = filepath.Clean(resolved)
	}
	if seen[key] {
		return nil, nil
	}
	seen[key] = true

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	var out []*SkillEntry
	for _, e := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		full := filepath.Join(dir, e.Name())
		if entry := readEntry(ctx, full, e, source, requireFlatMarker, depth, seen, maxDepth); entry != nil {
			out = append(out, entry)
		}
	}
	return out, nil
}

// readEntry classifies one directory entry: directory-layout skill,
// flat-layout skill (only when allowed), or a subdirectory to recurse
// into. Returns nil when the entry is neither a skill nor a recursable
// subdir.
func readEntry(ctx context.Context, full string, e os.DirEntry, source SkillSource, requireFlatMarker bool, depth int, seen map[string]bool, maxDepth int) *SkillEntry {
	isDir := e.IsDir()
	isFile := e.Type().IsRegular()
	if !isDir && !isFile {
		info, err := os.Stat(full)
		if err != nil {
			return nil
		}
		isDir = info.IsDir()
		isFile = info.Mode().IsRegular()
	}
	if isDir {
		if shouldSkipScanDir(e.Name()) {
			return nil
		}
		skillPath := filepath.Join(full, skillFile)
		if _, err := os.Stat(skillPath); err != nil {
			if depth >= maxDepth {
				return nil
			}
			children, _ := scanDir(ctx, full, source, requireFlatMarker, depth+1, seen, maxDepth)
			if len(children) == 0 {
				return nil
			}
			return nil
		}
		entry, err := loadFileSkill(ctx, os.ReadFile, skillPath, source)
		if err != nil {
			return nil
		}
		return entry
	}
	if isFile && strings.EqualFold(filepath.Ext(e.Name()), ".md") {
		if !requireFlatMarker {
			return nil
		}
		stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if stem == "" {
			return nil
		}
		entry, err := loadFileSkill(ctx, os.ReadFile, full, source)
		if err != nil {
			return nil
		}
		if !entryHasSkillMarker(entry) {
			return nil
		}
		return entry
	}
	return nil
}

// shouldSkipScanDir names a directory that must not be recursed into.
// These are either noise (dotfiles / dependency caches / build output) or
// reserved siblings whose contents are loaded by separate helpers
// (references / scripts / assets).
func shouldSkipScanDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "assets", "node_modules", "references", "scripts":
		return true
	default:
		return false
	}
}

// entryHasSkillMarker reports whether the loaded entry carries any
// frontmatter key that distinguishes a skill from generic markdown. Used
// to gate Claude-style flat files where <root>.md may be plain docs.
func entryHasSkillMarker(e *SkillEntry) bool {
	if e == nil {
		return false
	}
	if e.Description != "" || e.Version != "" || e.RunAs != "" {
		return true
	}
	if len(e.AllowedTools) > 0 || e.Model != "" || e.Effort != "" || e.ReadOnly || e.Color != "" {
		return true
	}
	if e.Invocation != "" && e.Invocation != "auto" {
		return true
	}
	if len(e.Triggers) > 0 || len(e.NegativeTriggers) > 0 || e.AutoUse != "" {
		return true
	}
	if e.NeedsFreshData || e.Cost != "" || len(e.Requires) > 0 || len(e.Profiles) > 0 {
		return true
	}
	return false
}

func loadEmbeddedSkills(ctx context.Context, filesystem embed.FS, root string) ([]*SkillEntry, error) {
	entries := make([]*SkillEntry, 0)
	err := fs.WalkDir(filesystem, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() || d.Name() != skillFile {
			return nil
		}
		entry, loadErr := loadFileSkill(ctx, filesystem.ReadFile, path, SkillSourceBundled)
		if loadErr != nil {
			return fmt.Errorf("load bundled skill %s: %w", path, loadErr)
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func loadFileSkill(ctx context.Context, readFile func(string) ([]byte, error), path string, source SkillSource) (*SkillEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := readFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSkillFileSize {
		return nil, fmt.Errorf("skill too large: %s", path)
	}
	fm, body, err := ParseFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	report, _ := ScanSkill(ctx, filepath.Dir(path))
	body = loadBodyWithReferences(path, body)
	body = loadBodyWithScripts(path, body)
	entry := &SkillEntry{
		ID:                     fm.Name,
		Name:                   fm.Name,
		Description:            fm.Description,
		Version:                fm.Version,
		Source:                 source,
		Path:                   path,
		Prompt:                 strings.TrimSpace(body),
		Enabled:                true,
		UserInvocable:          fm.UserInvocable,
		DisableModelInvocation: fm.DisableModelInvocation,
		LoadedAt:               time.Now(),
		RunAs:                  parseRunAs(fm.RunAs, fm.Context, fm.Agent),
		AllowedTools:           parseCSVFrontmatter(fm.AllowedTools),
		Model:                  strings.TrimSpace(fm.Model),
		Effort:                 strings.TrimSpace(fm.Effort),
		ReadOnly:               parseBoolFrontmatter(fm.ReadOnly),
		Color:                  strings.TrimSpace(fm.Color),
		Invocation:             parseInvocation(fm.Invocation),
		Triggers:               parseCSVFrontmatter(fm.Triggers),
		NegativeTriggers:       parseCSVFrontmatter(fm.NegativeTriggers),
		AutoUse:                parseAutoUse(fm.AutoUse),
		NeedsFreshData:         parseBoolFrontmatter(fm.NeedsFreshData),
		Cost:                   parseCost(fm.Cost),
		Requires:               parseCSVFrontmatter(fm.Requires),
	}
	entry.Profiles, entry.InvalidProfiles = parseProfilesFrontmatter(fm.Profiles)
	if report != nil {
		entry.RiskLevel = report.Level
		entry.RiskScore = report.Score
		entry.Findings = report.Findings
	}
	return entry, nil
}

// SkillSource identifies where a skill was loaded from.
type SkillSource string

const (
	SkillSourceBundled SkillSource = "bundled"
	SkillSourceProject SkillSource = "project"
	SkillSourceGlobal  SkillSource = "global"
	SkillSourceGitHub  SkillSource = "github"
	SkillSourceNPM     SkillSource = "npm"
)

// SkillEntry is one loaded skill: identity, prompt text, and the
// runtime-behaviour metadata parsed from its frontmatter.
type SkillEntry struct {
	ID                     string
	Name                   string
	Description            string
	Version                string
	Source                 SkillSource
	Path                   string
	Prompt                 string
	Enabled                bool
	UserInvocable          bool
	DisableModelInvocation bool
	RiskLevel              SecurityRiskLevel
	RiskScore              int
	Findings               []SecurityFinding
	LoadedAt               time.Time

	RunAs            string
	AllowedTools     []string
	Model            string
	Effort           string
	ReadOnly         bool
	Color            string
	Invocation       string
	Triggers         []string
	NegativeTriggers []string
	AutoUse          string
	NeedsFreshData   bool
	Cost             string
	Requires         []string
	Profiles         []string
	InvalidProfiles  []string
}

// SkillSourceLoader yields skill entries from one source: bundled,
// project, global, GitHub, or npm.
type SkillSourceLoader interface {
	LoadAll(ctx context.Context) ([]*SkillEntry, error)
}
