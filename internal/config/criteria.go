package config

// Criteria describes an option contrastively using the same fields for every family.
type Criteria struct {
	What     string   `toml:"what" json:"what"`
	NotFor   string   `toml:"not_for" json:"not_for"`
	Examples []string `toml:"examples" json:"examples"`
}

func DefaultCriteria() map[string]Criteria {
	return map[string]Criteria{
		"codex":  {"Implementation, tests, refactors and everyday coding", "Design, architecture, writing and harder reasoning belong to Claude", []string{"fix a flaky test", "refactor a scraper", "add a CLI command", "quick typo fix in a README"}},
		"claude": {"Design, architecture, writing and harder reasoning", "Everyday implementation, tests and refactors belong to Codex", []string{"draft a tech standard section", "design a multi-tenant auth model", "write an architecture proposal"}},
		"agy":    {"Tasks that explicitly ask for Antigravity or Gemini models via the agy CLI", "General implementation belongs to Codex; design, architecture and writing belong to Claude", []string{"try this in Antigravity", "use Gemini to compare approaches", "run this with agy"}},
		"grok":   {"Grok CLI tasks (placeholder; not launchable)", "Claude, Codex and Antigravity tasks", []string{"inspect Grok usage"}},
	}
}
