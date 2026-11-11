# switchboard v0.1

`sb` is a local Go CLI that execs genuine coding tools. It stores no credentials,
proxies no traffic, and never equates a login with available credits. Linux and
macOS are supported. Build locally with `make ci`; cross-build with `make darwin`.
Go 1.27.3, Kong, TOML and formatter versions are pinned in go.mod and Makefile.
Run `go mod download` before direct Go commands; dependency downloads require
network access. `make ci` performs this bootstrap automatically.
Do not install over existing launchers.

```sh
./build/sb --version
./build/sb --help
./build/sb claude work -- --version
./build/sb codex personal -- login status
./build/sb status --json
./build/sb doctor --plain
./build/sb --dry-run 'fix a flaky test'
./build/sb classify --json 'fix a flaky test'
./build/sb run 'implement the parser'
./build/sb cooldown codex/work --duration 1h
./build/sb usage --json
./build/sb schema --json
```

Explicit `claude` and `codex` selections default to work and bypass status,
routing, classifier and cooldowns. The optional work/personal selector is consumed;
the separator `--` is removed and remaining argv is forwarded unchanged. Put sb
flags before explicit launcher commands; everything after the separator belongs to
the tool. Claude maps CLAUDE_OAUTH_TOKEN_1 (work) or CLAUDE_OAUTH_TOKEN_2 (personal)
to CLAUDE_CODE_OAUTH_TOKEN and removes ANTHROPIC_API_KEY. Missing mapped tokens
fail safely. Existing shell/Keychain exports supply these values; sb does not
extract them. Codex sets CODEX_HOME to ~/.codex or ~/.codex-personal, creating it
only on an actual launch. Other environment variables are preserved.

`status` makes bounded genuine `claude auth status --json` and `codex login status`
checks for the four configured launcher identities. Only recognized booleans and
fixed diagnostics reach output. Unrecognized responses remain unknown. No live
account checks were used by the test suite. `doctor` checks executable presence,
exported variable presence, Codex directories, and on macOS Keychain service
presence using `security find-generic-password -s` without password flags. All
Keychain output is discarded. It does not install software, sign in, or repair anything.

Routing chooses a **family** first, then an account within that family. Rules use
first case-insensitive substring match, then `default` (built-in `codex/work`).
Both `default = "codex"` and legacy `default = "codex/work"` are valid; profiles
in defaults and rules are ignored. Remaining families follow stable Claude, Codex
order. Only positively signed-in accounts outside local cooldown enter selection.
Jev sees unique available launchable families, never profiles; AGY/Grok are not
launch targets. Status login is eligibility, not a claim of usable credits.

Within a family, binding used is the maximum known weekly/five-hour percentage;
headroom is 100 minus binding used. Any window at or over `usage_exhausted_pct`
(default 95, finite 0..100) exhausts an account. Known non-exhausted headroom ranks
first, then unknown usage (both windows null), then exhausted accounts. Extra
windows are not consulted in v0.1. All-exhausted families stay selected: choose the
earliest reset among windows at/over the cap; missing resets rank last. Other ties
use higher headroom, sooner relevant reset, then work before personal. Fall back
to another family only when none is signed in outside cooldown; none anywhere
exits 3. Unknown stays unknown and never fails a pick solely for missing usage.

`--account` > `SB_ACCOUNT` > TOML `pin` hard-pins an exact tool/profile, bypassing
Jev and usage selection. A signed-out, unknown-login or cooldown pin exits 3;
it never switches accounts. Explicit launcher commands retain their bypass.
Cooldown and pin values require full tool/profile; defaults/rules also accept
bare launchable families. Flags > environment > config > built-in defaults.

Copy `config.example.toml` manually to
$XDG_CONFIG_HOME/switchboard/config.toml (default ~/.config/switchboard/config.toml).
Unknown TOML keys fail. `account_floor` and `difficulty_floor` are finite 0..1,
default 0.5; legacy `confidence` maps as described below.

Routing reuses the read-only usage collectors for eligible launch profiles only,
with a two-second timeout per row (up to eight seconds for four eligible rows).
`sb usage` remains independent, eight rows, five seconds per row, without Jev.
Normalized numeric windows/reset times are cached in
$XDG_STATE_HOME/switchboard/usage-cache.json (default
~/.local/state/switchboard/usage-cache.json), mode 0600, parent 0700, atomic writes.
`usage_cache_ttl = "60s"` controls freshness (nonnegative duration). Fresh covered
rows skip collectors; expired/missing rows fetch. Failed/timed-out fetches reuse
entries no older than TTL plus five minutes, otherwise usage is unknown. Cache
writes are best effort and contain no task text, identity, raw blobs or secrets.
Dry-run/classify read the cache or fetch in memory and never write files, launch,
create homes or write cooldowns. The usage command does not use this routing cache.

The optional `[classifier]` table selects `provider = "typesafe"` (default),
`"ai-gateway"`, or `"off"`. Omitted fields use these provider defaults:

| Provider | base_url | model | api_key_env |
| --- | --- | --- | --- |
| typesafe | https://api.typesafe.ai | jev-1.14.0 | TYPESAFE_API_KEY |
| ai-gateway | https://ai-gateway.vercel.sh/typesafe | typesafe-ai/jev-1.14.0 | AI_GATEWAY_API_KEY |

Override `provider`, `base_url` and `model` with
`--classifier-provider`, `--classifier-base-url`, `--classifier-model`, or
`SB_CLASSIFIER_PROVIDER`, `SB_CLASSIFIER_BASE_URL`, `SB_CLASSIFIER_MODEL`.
Precedence is flags > environment > TOML > built-in defaults. Unknown providers,
invalid fields and unknown TOML keys fail with exit 2.

Keys resolve from the configured `api_key_env`, then optional `api_key_file`
(`~/` expands to home), then optional `api_key_keychain` (a macOS generic-password
service name). Sources are whitespace-trimmed; missing, unreadable or empty
sources fall through. Keychain password reads have a one-second timeout and
discard diagnostics. No key means rules only; `off` never resolves a key or calls
Jev. Doctor reports the configured provider and key presence only, using readable
file presence or a Keychain lookup without password flags; it makes no repairs.

**Routing, dry-run and `classify` send task text externally when a classifier
key is configured.** `sb classify --json "<task>"` uses routing eligibility and
cooldowns but never launches, creates homes or writes state. Status, doctor and
usage never call Jev. Requests use the pinned provider models above and a
three-second bound; model echoes accept equivalent short/prefixed IDs.

Choice uses contrastive family objects (`what`, `not_for`, `examples`). Codex
covers implementation, tests, refactors and everyday coding; Claude covers design,
architecture, writing and harder reasoning. Override fields with `[accounts.codex]`
or `[accounts.claude]`; provided fields replace individual built-in fields.
Unknown family IDs are accepted but have no effect until launchable. AGY/Grok
criteria are placeholders only. Empty merged `what` and unknown fields fail.

Score levels describe small local changes, scoped features/bugs/refactors, and
open-ended architecture or cross-system high-risk changes, each with examples.
Difficulty only informs reporting; it never changes account selection.
`probabilities[choice] >= account_floor` accepts an eligible Jev pick regardless
of Choice confidence. Difficulty is shown only when its own confidence meets
`difficulty_floor`; missing confidence leaves difficulty null. Invalid responses
fall back to rules. Floors are TOML settings in v0.1. The family probability default is
0.5, replacing the old implicit 0.8. Explicit `account_floor` wins over legacy
`confidence`; otherwise an explicitly supplied `confidence` maps to
`account_floor`, including zero. Difficulty defaults independently to 0.5.

Classify and routing dry-run JSON include family, tool, profile, account reason, family source, why,
probability, confidence, difficulty and difficulty_confidence. Rules have null
family probability/confidence. A valid response also appears under `jev` with
raw normalized choice and numbers even when below the floor; no HTTP bodies or
keys are exposed. `candidates` contains account ID, status login, cooldown, weekly/five-hour
percentages/resets, exhausted, headroom and chosen. Usage login does not determine
eligibility. Plain output is tab-separated pick, family, source, probability, confidence,
reason, why, followed by candidate rows. Tests use fake transports only; no live classification was performed.
See TypeSafe's [contrastive criteria cookbook](https://docs.typesafe.ai/concepts/how-to-build-with-system-one),
[probability routing](https://docs.typesafe.ai/patterns/probability-routing) and
[confidence routing](https://docs.typesafe.ai/patterns/confidence-routing).

Cooldowns are explicit local reports, not automatic 429 detection. Exec replaces
sb, so sb cannot observe subsequent tool output or exits. State is written
atomically with mode 0600 under $XDG_STATE_HOME/switchboard (default
~/.local/state/switchboard), with a mode 0700 parent. A dry cooldown report writes
nothing. No credential values or task text are persisted.

## Usage contract and safety

Usage uses independent adapters with a five-second timeout per row. Codex work
and personal use the official `codex -s read-only app-server` newline JSON-RPC
protocol: `initialize`, `initialized`, `account/rateLimits/read` with null params.
No thread or turn is created. Primary/secondary slots map by `windowDurationMins`:
300 is five hours, 10080 is weekly; other durations appear in `extra` as numeric
percentages and explicit reset times. Successful authenticated rate-limit reads
supply login state; login never implies usable credits. No account identity is
copied. Native CLIs handle their existing login; sb never reads credentials.

Claude runs only `auth status --json`, using isolated config directories
~/.local/share/claude-usage/{work,personal} and ~/.claude for plain. Token/API key
overrides are removed. These logins differ from the token-mapped `sb status`
identities. Claude quotas remain null with an explicit explanation because TUI
`/usage` is unsafe to automate. AGY pool rows remain null: `--print /usage` has
not been independently verified as a non-inference operation.

Grok invokes only `grok usage --json` without session/turn arguments. Failure,
non-JSON, or an unverified output schema yields sanitized errors and null quotas;
no service is started or repaired. The current adapter deliberately does not
invent mappings for an unverified Grok JSON schema.

Source provenance: the coordinator's local read-only research verified the exact
Codex protocol on both profiles and Grok's native usage help. The official
[Codex app-server protocol](https://github.com/openai/codex/tree/main/codex-rs/app-server)
describes the rate-limit operation. No live providers are invoked by tests;
fake executables and transports assert the command and RPC allowlists, timeouts,
window mapping, and isolation from task execution, status probing and classifier.

`Snapshot` remains an optional library source for dedicated normalized snapshots,
not the CLI default or an automatic fallback. It rejects unknown fields and
invalid numeric/time values and labels output `cached normalized snapshot`, with
`extra.observed_at` and `extra.age_seconds`. It never imports provider files.

`sb usage --json` always emits one complete eight-row document, including on
all-source failure (exit 1). Partial success exits 0 with per-row errors. Live
sources are labeled explicitly; missing or unsafe sources return null metrics
and fixed diagnostics. Plans are configured labels, not inferred subscriptions.
No prompts, private HTTP endpoints, trust/login dialogs, credential files or
session transcripts are accessed. No estimates are produced.

Stable version 1 shape (all entry keys are always present):

```json
{"schema_version":1,"checked_at":"2026-11-11T21:34:00+11:00","accounts":[{"tool":"claude","profile":"work","account":null,"plan":"Ultra","login_ok":null,"weekly_used_pct":null,"weekly_resets_at":null,"five_hour_used_pct":null,"five_hour_resets_at":null,"extra":{},"source":null,"error":"no verified safe source; quota unavailable"}]}
```

Actual reports contain exactly eight built-in rows: Claude work/personal (Ultra),
Codex work (Team) and personal (Plus), AGY Gemini and Claude/GPT pools, Grok CLI,
and plain Claude. `tool` is an extensible string, not a closed enum; consumers may
append tools such as grokbot. `checked_at` uses Australia/Sydney with the actual
offset; timezone data is embedded. Percentages are numbers 0..100 or null. Reset
strings require explicit RFC3339 timezones. `extra` is always an object.

JSON output is one document for reporting commands; `--plain` uses stable tab
separators without decoration. Executed tools own their subsequent stdout/stderr;
sb prints the actual routing decision to stderr before exec. `--no-input` is
accepted: sb never prompts. `NO_COLOR` and `TERM=dumb` need no special treatment
because sb emits no colors or terminal control sequences. Errors are concise,
fixed diagnostics; raw provider output is never forwarded by status or usage.
Exit codes: 0 success, 1 operational/source failure, 2 invalid input/config,
3 no eligible account. No telemetry.
