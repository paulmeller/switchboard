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
		"agy":    {"AGY CLI tasks (placeholder; not launchable)", "Claude and Codex tasks", []string{"inspect AGY usage"}},
		"grok":   {"Grok CLI tasks (placeholder; not launchable)", "Claude and Codex tasks", []string{"inspect Grok usage"}},
	}
}
