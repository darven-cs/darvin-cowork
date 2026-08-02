package skills

import (
	"context"
	"time"
)

type SkillSource string

const (
	SkillSourceBundled SkillSource = "bundled"
	SkillSourceUser    SkillSource = "user"
	SkillSourceGitHub  SkillSource = "github"
	SkillSourceNPM     SkillSource = "npm"
)

type SecurityRiskLevel string

const (
	RiskSafe     SecurityRiskLevel = "safe"
	RiskLow      SecurityRiskLevel = "low"
	RiskMedium   SecurityRiskLevel = "medium"
	RiskHigh     SecurityRiskLevel = "high"
	RiskCritical SecurityRiskLevel = "critical"
)

type SecurityFinding struct {
	Dimension string
	Severity  string
	Message   string
	File      string
	Line      int
}

type SecurityReport struct {
	Level    SecurityRiskLevel
	Score    int
	Findings []SecurityFinding
}

type SkillInvocation struct {
	UserInvocable          bool `yaml:"userInvocable"`
	DisableModelInvocation bool `yaml:"disableModelInvocation"`
}

type Frontmatter struct {
	Name        string          `yaml:"name"`
	Description string          `yaml:"description"`
	Version     string          `yaml:"version"`
	Invocation  SkillInvocation `yaml:"invocation"`
}

type SkillEntry struct {
	ID                     string
	Name                   string
	Description            string
	Version                string
	Source                 SkillSource
	Path                   string
	Prompt                 string
	Enabled                bool
	IsBuiltIn              bool
	IsOfficial             bool
	UserInvocable          bool
	DisableModelInvocation bool
	RiskLevel              SecurityRiskLevel
	RiskScore              int
	Findings               []SecurityFinding
	LoadedAt               time.Time
}

type SkillSourceLoader interface {
	LoadAll(ctx context.Context) ([]*SkillEntry, error)
}
