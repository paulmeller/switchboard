package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"switchboard/internal/decision"
	"switchboard/internal/picker"

	"switchboard/internal/classifier"
	"switchboard/internal/config"
	"switchboard/internal/output"
	"switchboard/internal/provider"
	"switchboard/internal/state"
	"switchboard/internal/usage"

	"github.com/alecthomas/kong"
)

const Version = "0.1.0"

type Launch struct {
	Args []string `arg:"" optional:"" sep:"none" help:"Optional configured profile (e.g. work, personal, or custom), then tool arguments (use --)."`
}
type CLI struct {
	ClassifierProvider string   `help:"Classifier: typesafe, ai-gateway or off." env:"SB_CLASSIFIER_PROVIDER"`
	ClassifierBaseURL  string   `help:"Classifier TypeSafe-compatible base URL." env:"SB_CLASSIFIER_BASE_URL"`
	ClassifierModel    string   `help:"Classifier model." env:"SB_CLASSIFIER_MODEL"`
	JSON               bool     `help:"Emit one JSON document."`
	Plain              bool     `help:"Stable tab-separated output."`
	NoInput            bool     `help:"Never prompt (sb never prompts)."`
	DryRun             bool     `help:"Select without launch, homes or state writes; may append decision log."`
	NoLog              bool     `help:"Disable decision logging for this invocation."`
	Version            bool     `help:"Print version."`
	Config             string   `help:"Config TOML path." env:"SB_CONFIG"`
	Account            string   `help:"Hard pin tool/profile; for feedback, correct the account." env:"SB_ACCOUNT"`
	Claude             Launch   `cmd:"" passthrough:"" help:"Balance Claude accounts and exec; optional profile pin."`
	Codex              Launch   `cmd:"" passthrough:"" help:"Balance Codex accounts and exec; optional profile pin."`
	Agy                Launch   `cmd:"" passthrough:"" help:"Balance configured Antigravity accounts and exec; optional profile pin."`
	Status             struct{} `cmd:"" help:"Check launch account login states, not credits."`
	Doctor             struct{} `cmd:"" help:"Check local prerequisites without changing them."`
	Usage              struct{} `cmd:"" help:"Read safe live usage; unknown values remain null."`
	Schema             struct{} `cmd:"" help:"Describe actual commands and output contracts."`
	Classify           struct {
		Task string `arg:"" help:"Task to classify without launching."`
	} `cmd:"" help:"Calibrate routing; may send task text to Jev."`
	Feedback struct {
		ID     string `arg:"" help:"Decision UUID or last."`
		OK     bool   `help:"Stored family and account were correct."`
		Family string `help:"Correct launchable family."`
	} `cmd:"" help:"Append a local decision label (even with --no-log)."`
	Tune struct{} `cmd:"" help:"Report local labelled routing accuracy and probability floors."`
	Run  struct {
		Task string `arg:"" help:"Task prompt (quote as one argument)."`
	} `cmd:"" help:"Route a task and exec the selected tool."`
	Cooldown struct {
		Account  string        `arg:"" help:"tool/profile"`
		Duration time.Duration `help:"Local cooldown duration after a 429." default:"1h"`
	} `cmd:"" help:"Explicitly report a local cooldown."`
}
type App struct {
	Out, Err    io.Writer
	Home        string
	Env         []string
	Probe       provider.Probe
	Execute     provider.Runner
	UsageSource usage.Source
	Classifier  classifier.Classifier
}

func Main(args []string) int {
	home, e := os.UserHomeDir()
	if e != nil {
		fmt.Fprintln(os.Stderr, "sb: cannot resolve home")
		return 2
	}
	return (&App{Out: os.Stdout, Err: os.Stderr, Home: home, Env: os.Environ(), Execute: provider.Run}).Run(args)
}

func (a *App) value(k string) string {
	for _, v := range a.Env {
		key, x, ok := strings.Cut(v, "=")
		if ok && key == k {
			return x
		}
	}
	return ""
}
func (a *App) fail(code int, msg string) int { fmt.Fprintln(a.Err, "sb: "+msg); return code }
func normalize(args []string) []string {
	known := map[string]bool{"feedback": true, "tune": true, "classify": true, "run": true, "status": true, "doctor": true, "usage": true, "schema": true, "claude": true, "codex": true, "agy": true, "cooldown": true}
	for i := 0; i < len(args); i++ {
		v := args[i]
		if v == "--config" || v == "--account" || v == "--classifier-provider" || v == "--classifier-base-url" || v == "--classifier-model" {
			i++
			continue
		}
		if v == "--" {
			out := append([]string{}, args[:i]...)
			out = append(out, "run")
			return append(out, args[i:]...)
		}
		if strings.HasPrefix(v, "-") {
			continue
		}
		if !known[v] {
			out := append([]string{}, args[:i]...)
			out = append(out, "run")
			return append(out, args[i:]...)
		}
		break
	}
	return args
}

func (a *App) emit(c CLI, v any, lines []string) int {
	if output.Write(a.Out, c.JSON, v, lines) != nil {
		return a.fail(1, "cannot write output")
	}
	return 0
}

func (a *App) statuses(ctx context.Context, reg provider.Registry) []provider.Status {
	p := a.Probe
	if p == nil {
		p = provider.Live{Home: a.Home, Env: a.Env}
	}
	out := []provider.Status{}
	for _, spec := range reg {
		out = append(out, p.Status(ctx, spec))
	}
	return out
}

type parserExit int

func (a *App) Run(args []string) (code int) {
	defer func() {
		if v := recover(); v != nil {
			if exit, ok := v.(parserExit); ok {
				code = int(exit)
			} else {
				panic(v)
			}
		}
	}()
	// Version is usable without a command. Tool argv after a command is untouched.
	versionOnly, jsonVersion, plainVersion := false, false, false
	for _, arg := range args {
		switch arg {
		case "--version":
			versionOnly = true
		case "--json":
			jsonVersion = true
		case "--plain":
			plainVersion = true
		case "--no-input":
		default:
			versionOnly = false
			goto parsedVersion
		}
	}
parsedVersion:
	if versionOnly {
		if jsonVersion && plainVersion {
			return a.fail(2, "choose --json or --plain")
		}
		return a.emit(CLI{JSON: jsonVersion}, map[string]string{"version": Version}, []string{Version})
	}
	var c CLI
	p, e := kong.New(&c, kong.Name("sb"), kong.Description("Local CLI launcher. Examples: sb --dry-run 'fix tests'; sb codex personal -- --version"), kong.Writers(a.Out, a.Err), kong.Exit(func(code int) { panic(parserExit(code)) }))
	if e != nil {
		return a.fail(2, "cannot initialize commands")
	}
	ctx, e := p.Parse(normalize(args))
	if e != nil {
		return a.fail(2, "invalid arguments; use sb --help")
	}
	if c.JSON && c.Plain {
		return a.fail(2, "choose --json or --plain")
	}
	if c.Version {
		return a.emit(c, map[string]string{"version": Version}, []string{Version})
	}
	command := ctx.Command()
	base := strings.Fields(command)
	if len(base) == 0 {
		return a.fail(2, "a command or task is required; use sb --help")
	}
	name := base[0]
	background := context.Background()
	if name == "schema" {
		return a.emit(c, map[string]any{"schema_version": 1, "commands": schemaNode(p.Model.Node), "exit_codes": map[string]int{"success": 0, "failure": 1, "invalid_input": 2, "no_eligible_account": 3}, "decision_log_contract": decisionContract(), "classification_contract": map[string]string{"id": "decision UUID when successfully logged; otherwise omitted", "family": "launchable tool family", "candidates": "normalized status, cooldown and usage account candidates", "reason": "account selection explanation", "source": "jev|rules", "probability": "chosen family probability|null", "confidence": "Choice confidence|null", "difficulty": "0..2 when independently confident|null", "difficulty_confidence": "0..1|null", "jev": "raw normalized classification when valid", "why": "safe decision explanation"}, "usage_schema_version": 1, "usage_tool": "extensible string", "usage_contract": map[string]any{
			"schema_version": 1, "row_count": "number of configured claude/codex/agy accounts + 2 (grok/cli, claude/plain)", "tool_accounts": "built-in claude/codex work/personal plus any configured claude/codex/agy entries in [[tool_accounts]]; agy uses configured accounts or implicit agy/personal", "row_timeout": "usage 5s and routing 2s per row; agy rows use 20s in both", "checked_at": "RFC3339 Australia/Sydney",
			"entry_keys": map[string]string{"tool": "extensible string", "profile": "string", "account": "string|null", "plan": "configured string|null", "login_ok": "boolean|null", "weekly_used_pct": "number 0..100|null", "weekly_resets_at": "RFC3339|null", "five_hour_used_pct": "number 0..100|null", "five_hour_resets_at": "RFC3339|null", "extra": "object", "source": "string|null", "error": "safe string|null", "checked_at": "RFC3339 UTC read/fetch time; cache rows use cache read time; error rows use attempt time"}, "sources": "live read-only Codex RPC, Claude login, Antigravity agy --output-format json --print /usage slash command, Grok usage; unsupported quotas null", "all_source_failure_exit": 1, "partial_success_exit": 0,
		}, "precedence": []string{"flags", "environment", "config", "defaults"}}, []string{"sb schema --json"})
	}
	if name == "feedback" || name == "tune" {
		// A routing environment pin is not an explicit feedback correction.
		if name == "feedback" {
			explicit := false
			for _, step := range ctx.Path {
				if step.Flag != nil && step.Flag.Name == "account" && !step.Resolved {
					explicit = true
				}
			}
			if !explicit {
				c.Account = ""
			}
		}
		return a.localReport(c, name)
	}
	if name == "usage" {
		reg, err := a.registry(c)
		if err != nil {
			return a.fail(2, err.Error())
		}
		source := a.UsageSource
		if source == nil {
			source = usage.Live(a.Home, a.Env, reg)
		}
		r, code := usage.Collect(background, source, reg, 5*time.Second, time.Now())
		lines := []string{"Usage (read-only sources; login is not a credit claim)"}
		for _, v := range r.Accounts {
			status := "available"
			if v.Error != nil {
				status = *v.Error
			}
			lines = append(lines, fmt.Sprintf("%s\t%s\tweekly=%s\tfive_hour=%s\t%s", v.Tool, v.Profile, percent(v.WeeklyUsedPct), percent(v.FiveHourUsedPct), status))
		}
		if e := a.emit(c, r, lines); e != 0 {
			return e
		}
		return code
	}
	if name == "status" {
		reg, err := a.registry(c)
		if err != nil {
			return a.fail(2, err.Error())
		}
		rows := a.statuses(background, reg)
		lines := []string{}
		code := 0
		for _, r := range rows {
			s := "unknown"
			if r.LoginOK != nil {
				s = "signed-out"
				if *r.LoginOK {
					s = "signed-in"
				}
			}
			if r.Error != nil {
				code = 1
			}
			diagnostic := "-"
			if r.Error != nil {
				diagnostic = *r.Error
			}
			lines = append(lines, r.Tool+"\t"+r.Profile+"\t"+s+"\t"+diagnostic)
		}
		if e := a.emit(c, map[string]any{"accounts": rows, "notice": "Login status does not imply usable credits."}, lines); e != 0 {
			return e
		}
		return code
	}
	if name == "doctor" {
		conf, err := a.loadConfig(c)
		if err != nil {
			return a.fail(2, err.Error())
		}
		checks := []map[string]any{}
		lines := []string{}
		code := 0
		add := func(name string, ok bool, fix string) {
			checks = append(checks, map[string]any{"check": name, "ok": ok, "fix": fix})
			lines = append(lines, fmt.Sprintf("%s\t%t\t%s", name, ok, fix))
			if !ok {
				code = 1
			}
		}
		add("classifier provider "+conf.Classifier.Provider, true, "")
		if conf.Classifier.Provider != "off" {
			present := (classifier.Keys{Home: a.Home, Env: a.value}).Present(conf.Classifier)
			fix := ""
			if !present {
				fix = "Set the configured classifier environment variable, readable key file, or macOS Keychain item; otherwise routing uses rules"
			}
			add("classifier key present", present, fix)
		}
		for _, tool := range []string{"claude", "codex", "agy"} {
			_, e := exec.LookPath(tool)
			add(tool+" binary", e == nil, "Install genuine CLI and add it to PATH")
		}
		for _, spec := range conf.Registry {
			switch spec.Tool {
			case "claude":
				if spec.ConfigDir != "" {
					info, e := os.Stat(provider.ClaudeDir(spec, a.Home))
					add(spec.ID()+" config_dir", e == nil && info.IsDir(), "Create the configured directory and sign in with CLAUDE_CONFIG_DIR=<dir> claude")
				} else if spec.TokenEnv != "" {
					add(spec.ID()+" "+spec.TokenEnv+" present", a.value(spec.TokenEnv) != "", "Export "+spec.TokenEnv+" in your shell")
				} else {
					key, service := "CLAUDE_OAUTH_TOKEN_1", "claude-oauth-work"
					if spec.Profile == "personal" {
						key, service = "CLAUDE_OAUTH_TOKEN_2", "claude-oauth-personal"
					}
					present := a.value(key) != ""
					if !present && runtime.GOOS == "darwin" {
						present = keychainPresent(service)
					}
					add(spec.ID()+" credentials present", present, "Export "+key+" or set config_dir/token_env in [[tool_accounts]]")
				}
			case "codex":
				info, e := os.Stat(provider.CodexHome(spec, a.Home))
				add("codex "+spec.Profile+" directory", e == nil && info.IsDir(), "Explicit sb codex launch creates the directory")
			case "agy":
				if spec.Home != "" {
					info, e := os.Stat(provider.AgyHome(spec, a.Home))
					add(spec.ID()+" home directory", e == nil && info.IsDir(), "Create the configured home and sign in with the genuine agy CLI using that HOME")
				}
			}
		}
		if e := a.emit(c, map[string]any{"checks": checks}, lines); e != 0 {
			return e
		}
		return code
	}
	if name == "claude" || name == "codex" || name == "agy" {
		explicit := false
		for _, step := range ctx.Path {
			if step.Flag != nil && step.Flag.Name == "account" && !step.Resolved {
				explicit = true
			}
		}
		return a.launchFamily(c, name, explicit)
	}
	statePath := state.Path(a.Home, a.value("XDG_STATE_HOME"))
	s, e := state.Load(statePath)
	if e != nil {
		return a.fail(2, e.Error())
	}
	if name == "cooldown" {
		reg, e := a.registry(c)
		if e != nil {
			return a.fail(2, e.Error())
		}
		account, e := reg.Parse(c.Cooldown.Account)
		if e != nil {
			return a.fail(2, e.Error())
		}
		if c.Cooldown.Duration <= 0 || c.Cooldown.Duration > 30*24*time.Hour {
			return a.fail(2, "duration must be positive and at most 720h")
		}
		until := time.Now().Add(c.Cooldown.Duration)
		if !c.DryRun {
			s[account.ID()] = until
			if state.Save(statePath, s) != nil {
				return a.fail(1, "cannot save cooldown")
			}
		}
		return a.emit(c, map[string]any{"account": account.ID(), "until": until.Format(time.RFC3339), "dry_run": c.DryRun}, []string{account.ID() + "\tcooldown reported"})
	}
	if name == "classify" {
		c.Run.Task = c.Classify.Task
		c.DryRun = true
	}
	if strings.TrimSpace(c.Run.Task) == "" {
		return a.fail(2, "task must not be empty")
	}
	conf, e := a.loadConfig(c)
	if e != nil {
		return a.fail(2, e.Error())
	}
	logged := false
	defer func() {
		if !logged {
			a.logDecision(c, conf, decision.Decision{Source: "rules", Reason: "routing failed before account selection"})
		}
	}()
	order, _ := conf.Order(c.Run.Task)
	now := time.Now()
	reason := ""
	pick := provider.Account{}
	source, why := "rules", "rules: classifier off"
	var raw classifier.Result
	candidates := []picker.Candidate{}
	family := ""
	if conf.Pin != "" {
		pick, _ = conf.ParseAccount(conf.Pin)
		p := a.Probe
		if p == nil {
			p = provider.Live{Home: a.Home, Env: a.Env}
		}
		r := p.Status(background, conf.Registry.Spec(pick))
		if r.LoginOK == nil || !*r.LoginOK || !s.Eligible(pick, now) {
			return a.fail(3, "pinned account signed out, unknown login or in cooldown")
		}
		candidates = append(candidates, picker.Candidate{Account: pick.ID(), Login: r.LoginOK, Chosen: true})
		family, source, why, reason = pick.Tool, "rules", "explicit account pin", "pinned account; usage selection bypassed"
	} else {
		statuses := a.statuses(background, conf.Registry)
		eligible := []string{}
		for _, f := range order {
			for _, r := range statuses {
				// Non-representative shared-pool members never add family capacity.
				if r.Tool == f && r.LoginOK != nil && *r.LoginOK && s.Eligible(r.Account, now) && conf.Registry.Auto(r.Account) {
					eligible = append(eligible, f)
					break
				}
			}
		}
		for _, f := range config.Fallback(order) {
			for _, e := range eligible {
				if f == e && family == "" {
					family = f
				}
			}
		}
		if len(eligible) == 0 || family == "" {
			return a.fail(3, "no eligible signed-in account; check sb status and local cooldowns")
		}
		if conf.Classifier.Provider != "off" {
			classify := a.Classifier
			why = "rules: no key"
			if classify == nil {
				key := (classifier.Keys{Home: a.Home, Env: a.value}).Resolve(conf.Classifier)
				if key != "" {
					classify = classifier.Jev{Key: key, Criteria: conf.Accounts, Endpoint: strings.TrimRight(conf.Classifier.BaseURL, "/") + "/v1/systemone", Model: conf.Classifier.Model}
				}
			}
			if classify != nil {
				raw = classify.Choose(background, c.Run.Task, eligible, conf.AccountFloor)
				why = "rules: invalid classifier response"
				if raw.OK && raw.Probability != nil && *raw.Probability >= 0 && *raw.Probability <= 1 {
					why = "rules: family probability below floor"
					if *raw.Probability >= conf.AccountFloor {
						why = "rules: classifier choice not eligible"
						for _, candidate := range eligible {
							if candidate == raw.Family {
								family = candidate
								reason = "Jev classification"
								source = "jev"
								why = reason
								break
							}
						}
					}
				}
			}
		}
		familyOrder := []string{family}
		for _, f := range config.Fallback(order) {
			if f != family {
				familyOrder = append(familyOrder, f)
			}
		}
		// Antigravity /usage is slow; fetch it only when Antigravity is selected.
		needed := []provider.Status{}
		for _, st := range statuses {
			if (st.Tool != "agy" || family == "agy") && conf.Registry.Auto(st.Account) {
				needed = append(needed, st)
			}
		}
		rows := a.selectionUsage(background, c, conf, needed, s, statePath, now)
		pick, reason, candidates = picker.Pick(conf.Registry, familyOrder, statuses, s, rows, conf.UsageExhaustedPct, conf.HarvestMinHeadroomPct, conf.HarvestWindowHours, now)
		if !pick.Valid() {
			return a.fail(3, "no eligible signed-in account; check sb status and local cooldowns")
		}
		if source == "jev" && pick.Tool != raw.Family {
			source = "rules"
			why = "rules: fallback from " + raw.Family + " (no eligible capacity)"
		}
		family = pick.Tool
	}
	var probability, confidence, difficulty, difficultyConfidence *float64
	if raw.OK {
		difficultyConfidence = raw.DifficultyConfidence
		if source == "jev" {
			probability, confidence = raw.Probability, raw.Confidence
		}
		if raw.DifficultyConfidence != nil && *raw.DifficultyConfidence >= conf.DifficultyFloor {
			difficulty = raw.Difficulty
		}
	}
	result := map[string]any{"family": family, "candidates": candidates, "tool": pick.Tool, "profile": pick.Profile, "reason": reason, "source": source, "why": why, "probability": probability, "confidence": confidence, "difficulty": difficulty, "difficulty_confidence": difficultyConfidence}

	logged = true
	d := decision.Decision{Record: decision.Record{Family: family, Account: pick.ID()}, Source: source, Probability: probability, Confidence: confidence, Difficulty: difficulty, DifficultyConfidence: difficultyConfidence, Reason: reason}
	if conf.Pin != "" {
		d.Label = &decision.Label{Family: family, Account: pick.ID()}
	}
	if c.DryRun {
		if id := a.logDecision(c, conf, d); id != "" {
			result["id"] = id
		}
	}

	if raw.OK {
		result["jev"] = map[string]any{"choice": raw.Family, "probability": raw.Probability, "confidence": raw.Confidence, "difficulty": raw.Difficulty, "difficulty_confidence": raw.DifficultyConfidence}
	}
	probabilityText := "null"
	if probability != nil {
		probabilityText = fmt.Sprintf("%g", *probability)
	}
	confidenceText := "null"
	if confidence != nil {
		confidenceText = fmt.Sprintf("%g", *confidence)
	}
	line := pick.ID() + "\t" + family + "\t" + source + "\t" + probabilityText + "\t" + confidenceText + "\t" + reason + "\t" + why

	if c.DryRun {
		lines := []string{line}
		for _, v := range candidates {
			login := "unknown"
			if v.Login != nil {
				login = fmt.Sprint(*v.Login)
			}
			reset := func(v *string) string {
				if v == nil {
					return "null"
				}
				return *v
			}
			lines = append(lines, fmt.Sprintf("candidate\t%s\tlogin=%s\tcooldown=%t\tweekly=%s\tweekly_reset=%s\t5h=%s\t5h_reset=%s\texhausted=%t\theadroom=%s\tchosen=%t", v.Account, login, v.Cooldown, percent(v.Weekly), reset(v.WeeklyReset), percent(v.Five), reset(v.FiveReset), v.Exhausted, percent(v.Headroom), v.Chosen))
		}
		return a.emit(c, result, lines)
	}
	fmt.Fprintln(a.Err, "sb: "+line)
	return a.launchAndLog(c, conf, d, conf.Registry.Spec(pick), provider.TaskArgs(pick, c.Run.Task))
}

func schemaNode(n *kong.Node) map[string]any {
	out := map[string]any{"name": n.Name, "help": n.Help}
	flags := []any{}
	for _, f := range n.Flags {
		flags = append(flags, map[string]any{"name": f.Name, "help": f.Help, "default": f.Default, "environment": f.Envs, "type": f.Target.Type().String()})
	}
	out["flags"] = flags
	args := []string{}
	for _, v := range n.Positional {
		args = append(args, v.Name)
	}
	out["arguments"] = args
	children := []any{}
	for _, child := range n.Children {
		children = append(children, schemaNode(child))
	}
	out["children"] = children
	return out
}

func percent(v *float64) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprintf("%g%%", *v)
}

func (a *App) configPath(c CLI) string {
	path := c.Config
	if path == "" {
		path = a.value("SB_CONFIG")
	}
	if path == "" {
		path = config.Path(a.Home, a.value("XDG_CONFIG_HOME"))
	}
	return path
}

// registry loads only the account set; routing pins and classifier overrides
// are irrelevant to status, usage and cooldown reports.
func (a *App) registry(c CLI) (provider.Registry, error) {
	conf, err := config.Load(a.configPath(c), "", "")
	return conf.Registry, err
}

func (a *App) loadConfig(c CLI) (config.Config, error) {
	return config.Load(a.configPath(c), a.value("SB_ACCOUNT"), c.Account,
		config.Classifier{Provider: a.value("SB_CLASSIFIER_PROVIDER"), BaseURL: a.value("SB_CLASSIFIER_BASE_URL"), Model: a.value("SB_CLASSIFIER_MODEL")},
		config.Classifier{Provider: c.ClassifierProvider, BaseURL: c.ClassifierBaseURL, Model: c.ClassifierModel})
}
