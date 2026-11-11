package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"switchboard/internal/config"
	"switchboard/internal/decision"
)

func (a *App) localReport(c CLI, name string) int {
	path := decision.Path(a.Home, a.value("XDG_DATA_HOME"))
	records, err := decision.Read(path)
	if err != nil {
		return a.fail(2, err.Error())
	}
	if name == "tune" {
		r := decision.Tune(records)
		lines := []string{fmt.Sprintf("decisions\t%d\nlabelled_decisions\t%d\ntoo_few_labels\t%t", r.Decisions, r.Labelled, r.TooFewLabels)}
		number := func(v *float64) string {
			if v == nil {
				return "null"
			}
			return fmt.Sprintf("%g", *v)
		}
		for _, f := range r.Families {
			lines = append(lines, fmt.Sprintf("family\t%s\tcorrect=%d\tn=%d\taccuracy=%s", f.Family, f.Correct, f.N, number(f.Accuracy)))
		}
		for _, f := range r.Floors {
			lines = append(lines, fmt.Sprintf("floor\t%g\tcorrect=%d\tn=%d\tcoverage=%g\taccuracy=%s", f.Floor, f.Correct, f.N, f.Coverage, number(f.Accuracy)))
		}
		if r.Recommendation == nil {
			lines = append(lines, "recommendation\tnone: no floor reaches 80% coverage")
		} else {
			lines = append(lines, "recommended_floor\t"+number(r.Recommendation))
		}
		if r.TooFewLabels {
			lines = append(lines, "Fewer than 30 labels; any recommendation is provisional, not proven.")
		}
		return a.emit(c, r, lines)
	}
	f := struct {
		ID              string
		OK              bool
		Family, Account string
	}{c.Feedback.ID, c.Feedback.OK, c.Feedback.Family, c.Account}
	if !f.OK && f.Family == "" && f.Account == "" {
		return a.fail(2, "feedback requires --ok, --family or --account")
	}
	if f.Family != "" && f.Family != "claude" && f.Family != "codex" {
		return a.fail(2, "invalid feedback family")
	}
	if f.Account != "" {
		account, err := config.ParseAccount(f.Account)
		if err != nil {
			return a.fail(2, "invalid feedback account")
		}
		if f.Family != "" && f.Family != account.Tool {
			return a.fail(2, "feedback family and account disagree")
		}
		f.Family = account.Tool
	}
	var target *decision.Decision
	for i := range records {
		d := &records[i]
		if d.Type == "decision" && (f.ID == "last" || f.ID == d.ID) {
			target = d
		}
	}
	if target == nil {
		return a.fail(2, "decision not found")
	}
	if f.OK {
		if (f.Family != "" && f.Family != target.Family) || (f.Account != "" && f.Account != target.Account) {
			return a.fail(2, "--ok conflicts with correction")
		}
		f.Family, f.Account = target.Family, target.Account
	}
	conf, err := a.loadConfig(c)
	if err != nil {
		return a.fail(2, err.Error())
	}
	record, err := decision.New("feedback")
	if err != nil {
		return a.fail(1, "cannot create feedback")
	}
	record.DecisionID, record.Family, record.Account = target.ID, f.Family, f.Account
	feedback := decision.Feedback{Record: record, OK: f.OK}
	if decision.Append(path, conf.Log.MaxBytes, feedback) != nil {
		return a.fail(1, "cannot append feedback")
	}
	return a.emit(c, feedback, []string{"feedback\t" + target.ID + "\t" + f.Family + "\t" + f.Account})
}

// Log before exec: successful replacement never returns to sb.
func (a *App) logDecision(c CLI, conf config.Config, d decision.Decision) string {
	if !conf.Log.Enabled || c.NoLog {
		return ""
	}
	record, err := decision.New("decision")
	if err == nil {
		record.Family, record.Account = d.Family, d.Account
		d.Record, d.DryRun = record, c.DryRun
		hash := sha256.Sum256([]byte(c.Run.Task))
		d.TaskSHA256 = hex.EncodeToString(hash[:])
		if !conf.Log.RedactTask {
			d.Task = &c.Run.Task
		}
		if conf.Pin != "" {
			d.Override = &decision.Override{Account: conf.Pin, Pin: true}
		}
		err = decision.Append(decision.Path(a.Home, a.value("XDG_DATA_HOME")), conf.Log.MaxBytes, d)
		if err == nil {
			return record.ID
		}
	}
	fmt.Fprintln(a.Err, "sb: cannot append decision log")
	return ""
}

func decisionContract() map[string]any {
	return map[string]any{
		"schema_version":  1,
		"path":            "$XDG_DATA_HOME/switchboard/decisions.jsonl (default ~/.local/share/switchboard/decisions.jsonl)",
		"permissions":     "directory 0700; live and .1 files 0600",
		"rotation":        "before exceeding max_bytes, replace one retained .1; records indivisible",
		"defaults":        map[string]any{"enabled": true, "max_bytes": 5242880, "redact_task": false},
		"id":              "UUID; included in dry-run/classify JSON on successful append",
		"ts":              "RFC3339 numeric offset in Australia/Sydney",
		"task":            "text or null when redacted; task_sha256 always hashes original bytes",
		"exit_code":       "always null; sb cannot observe replacement CLI exits",
		"duration_ms":     "always null, including dry-run/classify",
		"launches":        "all launches, including explicit family and passthrough; unless --no-log or log.enabled=false",
		"explicit_family": "boolean, always present; true for sb claude/codex, false for run/classify; old records default false",
		"implicit_labels": "explicit account pins only; defaults/rules never label",
		"feedback":        map[string]any{"target": "id|last across retained decisions; ignore feedback", "requires": "--ok or explicit --family/--account; corrections must agree", "writes": "even with --no-log or log.enabled=false", "fields": []string{"schema_version", "type", "id", "ts", "decision_id", "family", "account", "ok"}},
		"tune":            map[string]any{"sources": "retained local log only; latest label wins", "fields": []string{"schema_version", "decisions", "labelled_decisions", "jev_labelled_decisions", "too_few_labels", "families", "floors", "recommended_floor"}, "floors": []float64{.5, .6, .7, .8, .9}, "coverage": "labelled Jev with probability >= floor / all labelled Jev with probability", "accuracy": "correct / n; null when n=0", "recommendation": "highest accuracy with coverage >=0.80; ties higher floor; null if none", "minimum_labels": 30, "small_sample": "too_few_labels=true; provisional recommendation"},
	}
}
