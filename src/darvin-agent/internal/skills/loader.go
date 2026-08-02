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

type BundledSource struct {
	FS  embed.FS
	Dir string
}

func (s *BundledSource) LoadAll(ctx context.Context) ([]*SkillEntry, error) {
	return loadEmbeddedSkills(ctx, s.FS, s.Dir)
}

type UserSource struct {
	RootDir string
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

	entries := make([]*SkillEntry, 0)
	err := filepath.WalkDir(s.RootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}
		readFile := func(p string) ([]byte, error) { return os.ReadFile(p) }
		entry, loadErr := loadFileSkill(ctx, readFile, path, SkillSourceUser, false, false)
		if loadErr != nil {
			return nil
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
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
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		entry, loadErr := loadFileSkill(ctx, filesystem.ReadFile, path, SkillSourceBundled, true, true)
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

func loadFileSkill(ctx context.Context, readFile func(string) ([]byte, error), path string, source SkillSource, builtIn, official bool) (*SkillEntry, error) {
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
	report, scanErr := ScanSkill(ctx, filepath.Dir(path))
	entry := &SkillEntry{
		ID:                     fm.Name,
		Name:                   fm.Name,
		Description:            fm.Description,
		Version:                fm.Version,
		Source:                 source,
		Path:                   path,
		Prompt:                 strings.TrimSpace(body),
		Enabled:                true,
		IsBuiltIn:              builtIn,
		IsOfficial:             official,
		UserInvocable:          fm.Invocation.UserInvocable,
		DisableModelInvocation: fm.Invocation.DisableModelInvocation,
		LoadedAt:               time.Now(),
	}
	if scanErr == nil && report != nil {
		entry.RiskLevel = report.Level
		entry.RiskScore = report.Score
		entry.Findings = report.Findings
	}
	return entry, nil
}
