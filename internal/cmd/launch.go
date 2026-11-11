package cmd

import (
	"context"
	"path/filepath"
	"time"

	"switchboard/internal/config"
	"switchboard/internal/decision"
	"switchboard/internal/picker"
	"switchboard/internal/provider"
	"switchboard/internal/state"
	"switchboard/internal/usage"
)

func (a *App) launchFamily(c CLI, family string, accountFlag bool) int {
	argv := c.Claude.Args
	if family == "codex" {
		argv = c.Codex.Args
	}
	pin := ""
	if len(argv) > 0 && (argv[0] == "work" || argv[0] == "personal") {
		pin = family + "/" + argv[0]
		argv = argv[1:]
	}
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}
	if accountFlag {
		account, err := config.ParseAccount(c.Account)
		if err != nil || account.Tool != family {
			return a.fail(2, "--account must name a valid account in the selected family")
		}
		pin = account.ID()
	}
	conf, err := a.loadConfig(c)
	if err != nil {
		return a.fail(2, err.Error())
	}
	// Environment and TOML pins apply only to task routing.
	conf.Pin = pin
	logged := false
	defer func() {
		if !logged {
			a.logDecision(c, conf, decision.Decision{ExplicitFamily: true, Source: "rules", Reason: "routing failed before account selection"})
		}
	}()
	pick := provider.Account{}
	reason := "pinned account; usage selection bypassed"
	if pin != "" {
		pick, _ = config.ParseAccount(pin)
	} else {
		statePath := state.Path(a.Home, a.value("XDG_STATE_HOME"))
		s, err := state.Load(statePath)
		if err != nil {
			return a.fail(2, err.Error())
		}
		ctx, now := context.Background(), time.Now()
		statuses := []provider.Status{}
		for _, st := range a.statuses(ctx) {
			if st.Tool == family {
				statuses = append(statuses, st)
			}
		}
		rows := a.selectionUsage(ctx, c, conf, statuses, s, statePath, now)
		pick, reason, _ = picker.Pick([]string{family}, statuses, s, rows, conf.UsageExhaustedPct, now)
		if !pick.Valid() {
			return a.fail(3, "no eligible signed-in account; check sb status and local cooldowns")
		}
	}
	d := decision.Decision{Record: decision.Record{Family: family, Account: pick.ID()}, ExplicitFamily: true, Source: "rules", Reason: reason}
	if pin != "" {
		d.Label = &decision.Label{Family: family, Account: pick.ID()}
	}
	logged = true
	result := map[string]string{"tool": pick.Tool, "profile": pick.Profile, "reason": reason}
	if id := a.logDecision(c, conf, d); id != "" {
		result["id"] = id
	}
	if c.DryRun {
		return a.emit(c, result, []string{pick.ID() + "\t" + reason})
	}
	if err := provider.Launch(pick, argv, a.Home, a.Env, false, a.Execute); err != nil {
		return a.fail(1, err.Error())
	}
	return 0
}

func (a *App) selectionUsage(ctx context.Context, c CLI, conf config.Config, statuses []provider.Status, s state.State, statePath string, now time.Time) []usage.Row {
	needed := []usage.Row{}
	for _, r := range usage.Defaults() {
		for _, st := range statuses {
			if st.Tool == r.Tool && st.Profile == r.Profile && st.LoginOK != nil && *st.LoginOK && s.Eligible(st.Account, now) {
				needed = append(needed, r)
			}
		}
	}
	src := a.UsageSource
	if src == nil {
		src = usage.Live(a.Home, a.Env)
	}
	ttl, _ := time.ParseDuration(conf.UsageCacheTTL)
	return picker.Cached(ctx, src, filepath.Join(filepath.Dir(statePath), "usage-cache.json"), needed, ttl, 2*time.Second, now, !c.DryRun)
}
