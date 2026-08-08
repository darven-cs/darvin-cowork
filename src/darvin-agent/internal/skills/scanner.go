// Scans skill directories for SKILL.md files and parses their contents.

package skills

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	maxFiles         = 500
	maxFileSizeBytes = 512 * 1024
	maxFindings      = 100
	scanTimeout      = 5 * time.Second
	severityInfo     = 0
	severityWarning  = 5
	severityDanger   = 20
	severityCritical = 50
)

var skipDirs = map[string]struct{}{
	"node_modules": {},
	".git":         {},
	"__pycache__":  {},
	".svn":         {},
	".hg":          {},
	"dist":         {},
	"build":        {},
}

var (
	pyDangerPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\bsubprocess\.(run|Popen|call|check_output)\b`),
		regexp.MustCompile(`\bos\.system\b`),
		regexp.MustCompile(`\beval\s*\(`),
		regexp.MustCompile(`\bexec\s*\(`),
		regexp.MustCompile(`\burllib\.request\b`),
		regexp.MustCompile(`\brequests\.(get|post|put|delete)\b`),
	}
	shDangerPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\bcurl\s+`),
		regexp.MustCompile(`\bwget\s+`),
		regexp.MustCompile(`\brm\s+-rf\b`),
		regexp.MustCompile(`\bchmod\s+777\b`),
		regexp.MustCompile(`\beval\s+`),
		regexp.MustCompile(`\bnc\s+`),
	}
	shCriticalPatterns = []*regexp.Regexp{
		regexp.MustCompile(`curl\b[^;\n]*\|\s*(sh|bash)\b`),
		regexp.MustCompile(`wget\b[^;\n]*\|\s*(sh|bash)\b`),
		regexp.MustCompile(`rm\s+-rf\s+/\b`),
		regexp.MustCompile(`chmod\s+777\s+/`),
	}
	jsDangerPatterns = []*regexp.Regexp{
		regexp.MustCompile(`require\(['"]child_process['"]\)`),
		regexp.MustCompile(`\beval\s*\(`),
		regexp.MustCompile(`new\s+Function\s*\(`),
		regexp.MustCompile(`\bfetch\s*\(`),
		regexp.MustCompile(`XMLHttpRequest`),
	}
	goDangerousCalls = map[string]string{
		"exec.Command":   "process",
		"http.Get":       "network",
		"http.Post":      "network",
		"os.Remove":      "file_access",
		"unsafe.Pointer": "process",
	}
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityDanger   Severity = "danger"
	SeverityCritical Severity = "critical"
)

func ScanSkill(ctx context.Context, rootDir string) (*SecurityReport, error) {
	if _, err := os.Stat(rootDir); errors.Is(err, os.ErrNotExist) {
		return &SecurityReport{Level: RiskSafe, Score: 0, Findings: nil}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()

	report := &SecurityReport{Level: RiskSafe, Score: 0, Findings: []SecurityFinding{}}
	count := 0
	stop := errors.New("scan complete")

	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return stop
		}
		if d.IsDir() {
			if _, skip := skipDirs[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		if len(report.Findings) >= maxFindings {
			return stop
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if info.Size() > maxFileSizeBytes {
			return nil
		}
		rel, _ := filepath.Rel(rootDir, path)
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go":
			score, findings := scanGoFile(path, rel, 0, nil)
			report.Score += score
			report.Findings = append(report.Findings, findings...)
		case ".py":
			score, findings := scanRegexFile(path, rel, pyDangerPatterns, "dangerous_command", SeverityWarning, 0, nil)
			report.Score += score
			report.Findings = append(report.Findings, findings...)
		case ".sh", ".bash":
			criticalScore, criticalFindings := scanRegexFile(path, rel, shCriticalPatterns, "dangerous_command", SeverityCritical, 0, nil)
			report.Score += criticalScore
			report.Findings = append(report.Findings, criticalFindings...)
			// Pipe-to-shell patterns alone must exceed the high→critical
			// threshold; double the score so a single match crosses 70.
			for _, f := range criticalFindings {
				if strings.Contains(f.Message, "curl") || strings.Contains(f.Message, "wget") {
					report.Score += severityCritical
				}
			}
			score, findings := scanRegexFile(path, rel, shDangerPatterns, "dangerous_command", SeverityWarning, 0, nil)
			report.Score += score
			report.Findings = append(report.Findings, findings...)
		case ".js", ".mjs", ".cjs", ".ts":
			score, findings := scanRegexFile(path, rel, jsDangerPatterns, "dangerous_command", SeverityWarning, 0, nil)
			report.Score += score
			report.Findings = append(report.Findings, findings...)
		}
		count++
		if count >= maxFiles {
			return stop
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		return nil, err
	}
	if report.Score > 100 {
		report.Score = 100
	}
	report.Level = riskScoreToLevel(report.Score)
	if len(report.Findings) > maxFindings {
		report.Findings = report.Findings[:maxFindings]
	}
	return report, nil
}

func severityWeight(s Severity) int {
	switch s {
	case SeverityInfo:
		return severityInfo
	case SeverityWarning:
		return severityWarning
	case SeverityDanger:
		return severityDanger
	case SeverityCritical:
		return severityCritical
	default:
		return 0
	}
}

func riskScoreToLevel(score int) SecurityRiskLevel {
	switch {
	case score == 0:
		return RiskSafe
	case score <= 10:
		return RiskLow
	case score <= 30:
		return RiskMedium
	case score <= 70:
		return RiskHigh
	default:
		return RiskCritical
	}
}

func scanGoFile(path, rel string, score int, findings []SecurityFinding) (int, []SecurityFinding) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return score, findings
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		shortKey := sel.Sel.Name
		fullKey := ident.Name + "." + shortKey
		dim := ""
		if d, ok := goDangerousCalls[fullKey]; ok {
			dim = d
		} else if d, ok := goDangerousCalls[shortKey]; ok {
			dim = d
		}
		if dim == "" {
			return true
		}
		findings = append(findings, SecurityFinding{
			Dimension: dim,
			Severity:  string(SeverityDanger),
			Message:   fmt.Sprintf("call to %s", shortKey),
			File:      rel,
			Line:      fset.Position(call.Pos()).Line,
		})
		score += severityDanger
		return true
	})
	return score, findings
}

func scanRegexFile(path, rel string, patterns []*regexp.Regexp, dimension string, severity Severity, score int, findings []SecurityFinding) (int, []SecurityFinding) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return score, findings
	}
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		for _, p := range patterns {
			if p.MatchString(line) {
				findings = append(findings, SecurityFinding{
					Dimension: dimension,
					Severity:  string(severity),
					Message:   fmt.Sprintf("matched pattern: %s", p.String()),
					File:      rel,
					Line:      i + 1,
				})
				score += severityWeight(severity)
				break
			}
		}
	}
	return score, findings
}
