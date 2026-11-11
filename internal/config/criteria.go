package config

// Criteria describes an option contrastively using the same fields for every account.
type Criteria struct {
	What     string   `toml:"what" json:"what"`
	NotFor   string   `toml:"not_for" json:"not_for"`
	Examples []string `toml:"examples" json:"examples"`
}

func DefaultCriteria() map[string]Criteria {
	out := map[string]Criteria{}
	for _, profile := range []string{"work", "personal"} {
		scope, other := "Five V / company work repos", "Paul's own products: BorderProof, Service Desk, switchboard, Folio, Ironbark, Jacaranda and similar personal projects"
		coding, design := "fix a flaky test in a Five V service", "design a multi-tenant auth model for a work service"
		if profile == "personal" {
			scope, other = other, scope
			coding, design = "refactor the BorderProof visa scraper; add a command to switchboard", "design the architecture of a personal product"
		}
		out["codex/"+profile] = Criteria{"Implementation, tests, refactors and everyday coding in " + scope, other + "; design, architecture, writing and harder reasoning belong to claude/" + profile, []string{coding, "quick typo fix in a " + profile + " README"}}
		out["claude/"+profile] = Criteria{"Design, architecture, writing and harder reasoning in " + scope, other + "; everyday implementation, tests and refactors belong to codex/" + profile, []string{design, "draft a " + profile + " tech standard section"}}
	}
	out["agy"] = Criteria{"Non-launch usage-only tool; not used for sb routing yet", "Current sb task routing to Claude or Codex", []string{"inspect AGY usage"}}
	out["grok"] = Criteria{"Non-launch usage-only tool; not used for sb routing yet", "Current sb task routing to Claude or Codex", []string{"inspect Grok usage"}}
	return out
}
