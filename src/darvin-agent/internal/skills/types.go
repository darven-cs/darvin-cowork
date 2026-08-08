package skills

import (
	"context"
	"time"
)

type SkillSource string

const (
	SkillSourceBundled SkillSource = "bundled"
	SkillSourceProject SkillSource = "project"
	SkillSourceGlobal  SkillSource = "global"
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

type SkillSourceLoader interface {
	LoadAll(ctx context.Context) ([]*SkillEntry, error)
}
