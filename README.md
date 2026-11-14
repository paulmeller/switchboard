# switchboard v0.1

`sb` is a local Go CLI that launches genuine coding tools. It stores no credentials,
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
./build/sb agy work -- --help
./build/sb --dry-run agy
./build/sb status --json
./build/sb doctor --plain
./build/sb --dry-run 'fix a flaky test'
./build/sb classify --json 'fix a flaky test'
./build/sb run 'implement the parser'
./build/sb cooldown codex/work --duration 1h
./build/sb usage --json
./build/sb feedback last --ok
./build/sb feedback last --family claude
./build/sb tune --json
./build/sb schema --json
```

Explicit `claude`, `codex` and `agy` commands balance signed-in accounts outside cooldown
within the named family using usage/reset ranking, without a classifier or family
fallback. Only an explicit `--account tool/profile` flag or positional profile
selector (work/personal; for agy a configured profile) pins an account and bypasses status/usage/cooldown checks. The flag must
match the named family; `SB_ACCOUNT` and TOML `pin` apply only to task routing.
No eligible account in the named family exits 3. The optional profile
selector and separator `--` are removed; remaining argv is forwarded unchanged. Put sb
flags before explicit launcher commands; everything after the separator belongs to
the tool. Claude maps CLAUDE_OAUTH_TOKEN_1 (work) or CLAUDE_OAUTH_TOKEN_2 (personal)
to CLAUDE_CODE_OAUTH_TOKEN and removes ANTHROPIC_API_KEY. Alternatively, Claude entries in `[[tool_accounts]]` may set `config_dir` to an
absolute or `~/` directory. This sets CLAUDE_CONFIG_DIR and strips both profile
tokens, CLAUDE_CODE_OAUTH_TOKEN and ANTHROPIC_API_KEY. The directory must exist
on real launches; sign in with `CLAUDE_CONFIG_DIR=<dir> claude`. Without config_dir,
missing mapped tokens fail safely. Existing shell/Keychain exports supply these values; sb does not
extract them. Codex sets CODEX_HOME to ~/.codex or ~/.codex-personal, creating it
only on an actual launch. Other environment variables are preserved.

Antigravity (`agy`) accounts are declared in the sb config, not in code. Each
`[[tool_accounts]]` entry names one account; every one runs the genuine `agy` on PATH:

```toml
[[tool_accounts]]
id = "agy/work"                  # required; agy/<profile>; `sb agy work` pins it
label = "Antigravity (work)"     # required; safe display label, never an email

[[tool_accounts]]
id = "agy/second"
label = "Antigravity (second)"
home = "/path/to/agy-second-profile" # optional; absolute or ~/...
quota_pool = "shared-pool-id"        # optional; opaque shared-allowance id
```

With no `tool_accounts`, sb uses one implicit `agy/personal` ("Antigravity", no
home, no pool), so `sb agy`, `sb agy personal` and `--account agy/personal` work
out of the box. Declaring any account replaces the implicit one. Only `agy/...`
ids are accepted (Claude/Codex accounts are built in); empty or bare `agy` ids,
missing labels, duplicate ids, relative homes and unknown fields fail with exit 2.
Adding or removing an account is a config edit only.

An account without `home` keeps the process HOME and environment unchanged. With
`home`, sb sets `HOME` to it, `AGY_REAL_HOME` to the original HOME (unless already set),
`XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME` and `XDG_STATE_HOME` to
`.config`, `.local/share`, `.cache` and `.local/state` under it, and umask 077, matching
an isolated-profile wrapper. sb never creates, reads or copies profile files.
`sb agy` balances auto-eligible accounts; `sb agy <profile>` or
`--account agy/<profile>` pins. Routed tasks start `agy --prompt-interactive "<task>"`.

Shared quota pools: accounts with the same `quota_pool` share one allowance, so sb
never uses another member to gain capacity. Automatic selection (unpinned `sb agy`,
routed tasks) only considers the pool representative, the first configured
member. If the representative is at or over `usage_exhausted_pct`, it is skipped
and no other member is tried: routing falls through to another family, and
`sb agy` exits 3 unless another independent agy account is eligible. Other pool
members are used only when pinned. Each pooled account still gets its own
`sb usage` row with `extra.quota_pool`; dry-run candidates show `quota_pool` and
`skipped`. Accounts in different pools, or none, are independent.

`status` makes bounded genuine `claude auth status --json` and `codex login status`
checks for the four Claude/Codex launcher identities. Antigravity status runs no
subprocess and sends no prompt: it requires `agy` on PATH and reports signed-in when
`<account HOME>/.gemini/antigravity-cli/antigravity-oauth-token` exists (stat only;
contents are never opened). A present marker can still be an expired sign-in, so
one row per launch account (four Claude/Codex plus each Antigravity account) is
reported but none implies credits. Only recognized booleans and
fixed diagnostics reach output. Unrecognized responses remain unknown. No live
account checks were used by the test suite. `doctor` checks executable presence (claude, codex, agy),
exported variable presence, Codex directories, Antigravity account HOME directories, and on macOS Keychain service
presence using `security find-generic-password -s` without password flags. All
Keychain output is discarded. It does not install software, sign in, or repair anything.

Routing chooses a **family** first, then an account within that family. Rules use
first case-insensitive substring match, then `default` (built-in `codex/work`).
Both `default = "codex"` and legacy `default = "codex/work"` are valid; profiles
in defaults and rules are ignored. Remaining families follow stable Claude, Codex
order. Only positively signed-in accounts outside local cooldown enter selection.
Jev sees unique available launchable families (claude, codex, agy), never profiles;
Grok is not a launch target. Antigravity is used only when it is the selected
family (a rule/default naming `agy`, a Jev choice, or a pin); it is never an
automatic fallback when Claude/Codex are unavailable. Status login is eligibility, not a claim of usable credits.

Within a family, binding used is the maximum known weekly/five-hour percentage;
headroom is 100 minus binding used. The binding window has the higher usage;
equal usage binds to the window with the sooner known reset. Any known window at
or over `usage_exhausted_pct` (default 95, finite 0..100) sets the exhausted flag.
Rank under-cap accounts with headroom > 0 first, unknown usage (both windows null)
next, exhausted accounts with headroom > 0 next, then fully used accounts
(headroom <= 0). Extra windows are not consulted in v0.1.

Within the under-cap tier, harvest accounts with headroom at least
`harvest_min_headroom_pct` (default 10, finite 0..100) whose binding window's own
reset is known, in the future and within `harvest_window_hours` (default 6, finite
and >= 0; zero disables harvesting). Prefer the soonest binding reset among these
accounts; otherwise prefer the most headroom. Under-cap accounts below the minimum
still rank above unknown usage. Exhausted accounts with remaining quota prefer
most headroom. Fully used accounts prefer the soonest known binding reset, with
missing resets last. Ties prefer work before personal (Antigravity: config order). Task routing falls back to
another family only when none is signed in outside cooldown, or when the selected
family's only auto-eligible candidates are exhausted shared-pool representatives;
none anywhere exits 3.
Unknown stays unknown and never fails a pick solely for missing usage.

`--account` > `SB_ACCOUNT` > TOML `pin` hard-pins an exact tool/profile, bypassing
Jev and usage selection. A signed-out, unknown-login or cooldown pin exits 3;
it never switches accounts. Explicit launcher pins use the bypass described above.
Cooldown and pin values require full tool/profile; defaults/rules also accept
bare launchable families. Flags > environment > config > built-in defaults.

Copy `config.example.toml` manually to
$XDG_CONFIG_HOME/switchboard/config.toml (default ~/.config/switchboard/config.toml).
Unknown TOML keys fail. `account_floor` and `difficulty_floor` are finite 0..1,
default 0.5; legacy `confidence` maps as described below.

Routing reuses the read-only usage collectors for eligible launch profiles only,
with a two-second timeout per row; Antigravity rows use 20 seconds and are fetched
only when Antigravity is the selected family, and only for auto-eligible accounts
(pool representatives and unpooled accounts), so an unpinned `sb agy` can wait up
to 20 seconds per such account on a cold cache. `sb usage` remains independent,
6 + N_agy rows (7 with the implicit default), five
seconds per row (Antigravity 20 seconds), without Jev.
Normalized numeric windows/reset times are cached in
$XDG_STATE_HOME/switchboard/usage-cache.json (default
~/.local/state/switchboard/usage-cache.json), mode 0600, parent 0700, atomic writes.
`usage_cache_ttl = "60s"` controls freshness (nonnegative duration). Fresh covered
rows skip collectors; expired/missing rows fetch. Failed/timed-out fetches reuse
entries no older than TTL plus five minutes, otherwise usage is unknown. Cache
writes are best effort and contain no task text, identity, raw blobs or secrets.
Dry-run/classify read the cache or fetch in memory and never launch, create homes,
or write cooldown/usage-cache state. They may append the local decision log unless
`--no-log` is supplied or logging is disabled. The usage command does not use this routing cache.

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
cooldowns but never launches, creates homes or writes cooldown/usage-cache state;
it may append a decision log line. Status, doctor and
usage never call Jev. Requests use the pinned provider models above and a
three-second bound; model echoes accept equivalent short/prefixed IDs.

Choice uses contrastive family objects (`what`, `not_for`, `examples`). Codex
covers implementation, tests, refactors and everyday coding; Claude covers design,
architecture, writing and harder reasoning. Override fields with `[accounts.codex]`
or `[accounts.claude]`; provided fields replace individual built-in fields.
Unknown family IDs are accepted but have no effect until launchable. AGY criteria
cover tasks that explicitly ask for Antigravity/Gemini; Grok criteria are a placeholder. Empty merged `what` and unknown fields fail.

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

Cooldowns are explicit local reports, not automatic 429 detection. sb waits for
the tool and returns its exit code; it does not inspect tool output. State is written
atomically with mode 0600 under $XDG_STATE_HOME/switchboard (default
~/.local/state/switchboard), with a mode 0700 parent. A dry cooldown report writes
nothing. Cooldown state contains no credential values or task text.


## Local decision log and calibration

Routed tasks, explicit `sb claude`/`sb codex`/`sb agy` launches (including passthrough
arguments), all dry-runs and `sb classify`
append version 1 JSONL to `$XDG_DATA_HOME/switchboard/decisions.jsonl`, default
`~/.local/share/switchboard/decisions.jsonl`. The switchboard directory is 0700;
the live file and retained `decisions.jsonl.1` are 0600. Before an append would
exceed `[log] max_bytes` (default 5242880, positive integer), the live file is
renamed to `.1`, replacing the previous generation. A record is never split;
a single oversized record can exceed the limit. Only these two generations are
retained. Concurrent rotation is not coordinated between processes.

`[log] enabled = true` and `redact_task = false` are the defaults. Unknown fields
and invalid values fail config loading with exit 2. `--no-log` disables decision
logging for that invocation. With `redact_task = true`, `task` is null and
`task_sha256` contains the lowercase SHA-256 of the original task bytes; the hash
is also present for unredacted tasks. Do not put secrets in task text. There are
no key, environment, HTTP, Keychain, candidate or usage dumps in this log.

Each decision includes a UUID `id`, Sydney RFC3339 timestamp with numeric offset,
dry-run flag, `explicit_family` boolean, selected family/account, source, nullable
classification metrics, reason and nullable override. Dry-run/classify JSON includes
`id` when appended.
Explicit account pins (`--account`, `SB_ACCOUNT`, TOML `pin`) include an override
and an implicit `label` for that exact family/account. Defaults and rules never
create implicit labels. Explicit family launches set `explicit_family=true` and
`source="rules"`; only their explicit flag/positional pins add an override and
implicit label. Unpinned usage picks have no label. Routed/classify decisions set
`explicit_family=false`; older records default to false. Explicit launch argv and
environment are never logged: task is empty (null when redacted), with the hash of
empty task bytes. Failed selection attempts can have empty family/account and no
label. Status, doctor, usage, schema and cooldown never append decisions.

Real launches write one decision after the child exits, recording `exit_code`
and elapsed `duration_ms`. Signal exits use 128 + signal. Launch failures record
exit code 1 and a safe failure reason followed by the selection reason.
Dry-run/classify keep both fields null.
Failed log writes produce a fixed stderr diagnostic and preserve routing's exit.
Dry-run's intentional logging exception creates no Codex homes or cooldown/usage
cache state; use `--no-log` for a selection without that log write.

`sb feedback <id|last> --ok` appends a feedback record accepting the stored family
and account. `last` ignores feedback records. Use `--family claude` or
`--account codex/personal` to correct a decision; an account correction also labels
its family. Families must be claude/codex/agy; accounts must be valid launch accounts.
`SB_ACCOUNT` alone is not a feedback correction. Corrections must agree with each other; `--ok` cannot conflict with a correction.
Missing/unknown IDs, empty logs and missing labels exit 2. Explicit feedback still
writes with `--no-log` or logging disabled, provided the target decision exists.
Feedback has its own UUID, timestamp, decision ID, family/account and `ok` boolean,
without task text. It uses the same permissions and rotation as decisions.

`sb tune [--json|--plain]` reads only the retained local log generations. It makes
no classifier, usage or provider calls and does not change `account_floor`.
Missing logs report zero decisions/labels and no recommendation. The latest
feedback replaces an implicit or earlier label for each decision. Labels whose
decisions have rotated out cannot contribute. Family accuracy groups by the
**predicted** family: correct labels / labelled predictions for that family.

For labelled Jev decisions with a probability, floors 0.5/0.6/0.7/0.8/0.9 report
coverage (number at/above floor divided by all such labelled Jev decisions) and
accuracy (correct family labels divided by predictions at/above floor).
Accuracy is null for an empty denominator. Among floors with coverage >= 80%,
tune recommends the highest accuracy, breaking ties toward the higher floor.
If none reaches 80%, there is no recommendation. With fewer than 30 family labels,
`too_few_labels` is true and any recommendation is explicitly provisional, not
proven. Pin labels measure explicit operator preferences and can inflate accuracy;
use representative corrections before changing `account_floor` manually.

## Usage contract and safety

Usage uses independent adapters with a five-second timeout per row (Antigravity 20s). Codex work
and personal use the official `codex -s read-only app-server` newline JSON-RPC
protocol: `initialize`, `initialized`, `account/rateLimits/read` with null params.
No thread or turn is created. Primary/secondary slots map by `windowDurationMins`:
300 is five hours, 10080 is weekly; other durations appear in `extra` as numeric
percentages and explicit reset times. Successful authenticated rate-limit reads
supply login state; login never implies usable credits. No account identity is
copied. Native CLIs handle their existing login; sb never reads credentials.

Claude runs only `auth status --json`, using isolated config directories
from configured `config_dir`, falling back to
~/.local/share/claude-usage/{work,personal}, and ~/.claude for plain. Token/API key
overrides are removed. Configured directories are shared with `sb status`; default usage logins may
differ from token-mapped status identities. Claude quotas remain null with an explicit explanation because TUI
`/usage` is unsafe to automate.

Antigravity rows are keyed by configured account (for example `agy/work`,
`agy/second`; default `agy/personal`) so routing can use them; `account` is the
configured label, never an email, plans stay null, and `extra.quota_pool` carries
the configured pool id when set. Each row runs only
`agy --output-format json --print /usage` under that account's environment (the
isolated HOME/XDG when the account has `home`, otherwise the process HOME). Print mode expands built-in slash commands
(`--disable-slash-commands` exists to turn that off; agy logs "running slash
command /usage"), so this is the built-in usage command, not a model prompt; no
other text is ever sent and `/credits` is not called. The last column-zero JSON
object in stdout is parsed (leading log lines are ignored); stderr is discarded.
The "Gemini Models" group, else the first group with `5h`/`weekly` buckets, supplies
`five_hour_*`/`weekly_*` with `used_pct = (1 - remaining_fraction) * 100` clamped
0..100 and resets in Australia/Sydney. Other groups and windows (Claude/GPT and any
other pools) appear under `extra.pools` with `extra.primary_pool` naming the primary.
A successful parse sets `login_ok` true. Failure, timeout, non-success status or
no groups leave all values null with a fixed error. Windows the payload omits stay null.

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

`sb usage --json` always emits one complete document with every row, including on
all-source failure (exit 1). Partial success exits 0 with per-row errors. Live
sources are labeled explicitly; missing or unsafe sources return null metrics
and fixed diagnostics. Plans are configured labels, not inferred subscriptions.
No prompts, private HTTP endpoints, trust/login dialogs, credential files or
session transcripts are accessed. No estimates are produced.

Stable version 1 shape (all entry keys are always present):

```json
{"schema_version":1,"checked_at":"2026-11-11T21:34:00+11:00","accounts":[{"tool":"claude","profile":"work","account":null,"plan":"Ultra","login_ok":null,"weekly_used_pct":null,"weekly_resets_at":null,"five_hour_used_pct":null,"five_hour_resets_at":null,"extra":{},"source":null,"error":"no verified safe source; quota unavailable"}]}
```

Actual reports contain 6 + N_agy rows in this order: Claude work/personal (Ultra),
Codex work (Team) and personal (Plus), one row per configured Antigravity account
in config order (or the implicit `agy/personal`), Grok CLI, and plain Claude. `tool` is an extensible string, not a closed enum; consumers may
append tools such as grokbot. `checked_at` uses Australia/Sydney with the actual
offset; timezone data is embedded. Percentages are numbers 0..100 or null. Reset
strings require explicit RFC3339 timezones. `extra` is always an object.

JSON output is one document for reporting commands; `--plain` uses stable tab
separators without decoration. Executed tools own their subsequent stdout/stderr;
sb prints the actual routing decision to stderr before launch. `--no-input` is
accepted: sb never prompts. `NO_COLOR` and `TERM=dumb` need no special treatment
because sb emits no colors or terminal control sequences. Errors are concise,
fixed diagnostics; raw provider output is never forwarded by status or usage.
Exit codes: 0 success, 1 operational/source failure, 2 invalid input/config,
3 no eligible account. No telemetry.
