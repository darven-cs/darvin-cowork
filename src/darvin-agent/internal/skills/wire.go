package skills

// SkillSummaryWire is the JSON shape main / renderer consume. Field tags
// match DarvinSkillSummary in src/shared/darvin-api.ts.
type SkillSummaryWire struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Version       string            `json:"version,omitempty"`
	Enabled       bool              `json:"enabled"`
	UserInvocable bool              `json:"userInvocable"`
	Path          string            `json:"path"`
	Source        string            `json:"source"`
	UpdatedAt     int64             `json:"updatedAt"`
	RiskLevel     string            `json:"riskLevel,omitempty"`
	RiskFindings  []RiskFindingWire `json:"riskFindings,omitempty"`
}

// ToSummary projects a registry entry to its renderer-facing wire shape.
// UpdatedAt falls back to LoadedAt (set at parse time) so renderer-side
// staleness ordering still has a value when the file hasn't been touched.
func ToSummary(e *SkillEntry) SkillSummaryWire {
	if e == nil {
		return SkillSummaryWire{}
	}
	risk := string(e.RiskLevel)
	if risk == "" {
		risk = string(RiskSafe)
	}
	ws := SkillSummaryWire{
		ID:            e.ID,
		Name:          e.Name,
		Description:   e.Description,
		Version:       e.Version,
		Enabled:       e.Enabled,
		UserInvocable: e.UserInvocable,
		Path:          e.Path,
		Source:        string(e.Source),
		UpdatedAt:     e.LoadedAt.UnixMilli(),
		RiskLevel:     risk,
	}
	if len(e.Findings) > 0 {
		ws.RiskFindings = make([]RiskFindingWire, 0, len(e.Findings))
		for _, f := range e.Findings {
			ws.RiskFindings = append(ws.RiskFindings, RiskFindingWire{
				Dimension: f.Dimension,
				Severity:  f.Severity,
				Message:   f.Message,
				File:      f.File,
				Line:      f.Line,
			})
		}
	}
	return ws
}

// ToSummaries projects a slice of entries.
func ToSummaries(entries []*SkillEntry) []SkillSummaryWire {
	out := make([]SkillSummaryWire, 0, len(entries))
	for _, e := range entries {
		out = append(out, ToSummary(e))
	}
	return out
}

// RiskFindingWire is the JSON shape renderer consumes for one scanner
// finding. Mirrors SecurityFinding but with explicit JSON tags.
type RiskFindingWire struct {
	Dimension string `json:"dimension"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	File      string `json:"file"`
	Line      int    `json:"line"`
}