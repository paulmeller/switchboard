# switchboard v0.1 (`sb`)

## Purpose and boundaries
A local-first Go CLI that selects among your own coding CLI logins and launches the genuine tool with exec. No provider traffic proxy, token store, OAuth extraction/refresh implementation, or subscription API emulation. Pluggable provider adapters; honest support documentation.

## CLI contract
Use Kong, `cmd/sb` and `internal/`, colocated tests, gofumpt/goimports. Data to stdout, diagnostics to stderr. Human default, stable `--plain`, one-document `--json`, `--version`, `--no-input`, `--dry-run`, help and NO_COLOR. Validate before acting. Exit 0 success, 1 operation failure, 2 usage/config validation, 3 no eligible account. `sb schema --json` describes the actual command tree and contracts. Linux and macOS, including darwin/arm64. `make ci` = pinned formatting check, vet, tests and build. No secrets in output, errors, schema, config, tests, subprocess diagnostics, or logs.

## Launcher behavior
Launchers default to `work` when no profile is given, consume `work`/`personal` if present, forward all remaining argv unchanged, resolve the real executable on PATH, then exec.
* `sb claude [work|personal] [-- args]`: work CLAUDE_OAUTH_TOKEN_1 or personal CLAUDE_OAUTH_TOKEN_2 maps to CLAUDE_CODE_OAUTH_TOKEN; ANTHROPIC_API_KEY must be absent. Missing token fails without printing it. Other env/argv preserved. On macOS ~/.zshrc exports these env vars from Keychain services claude-oauth-work / claude-oauth-personal; doctor may check presence but never output a value. No new direct secret extraction.
* `sb codex [work|personal] [-- args]`: CODEX_HOME is ~/.codex or ~/.codex-personal, mkdir -p if needed, then syscall.Exec. Never inspect auth files. Note existing codex-as currently omits mkdir; this requested improvement is intentional. Explicit family commands balance within that family using status, cooldowns and usage, without a classifier or family fallback. Only an explicit --account tool/profile flag (validated to match the family) or positional work/personal selector pins and bypasses status/usage/cooldown checks. SB_ACCOUNT and TOML pin apply only to task routing.
* `sb agy [<profile>] [-- args]` (Antigravity): every Antigravity account execs the genuine `agy` found on PATH. Accounts come from config `[[tool_accounts]]` entries (`id = "agy/<profile>"`, required safe `label`, optional `home`, optional `quota_pool`); with none configured there is one implicit `agy/personal` (label "Antigravity", no home, no pool). No Antigravity account is hard-coded beyond that default. An account without `home` leaves the process environment and HOME unchanged. An account with `home` mirrors the isolated-profile launcher: HOME = that directory (absolute, or `~/` relative to the process home), AGY_REAL_HOME = original HOME unless already set, XDG_CONFIG_HOME/XDG_DATA_HOME/XDG_CACHE_HOME/XDG_STATE_HOME under it (`.config`, `.local/share`, `.cache`, `.local/state`), umask 077 at exec. sb never creates, reads or copies profile/credential files. A positional configured profile or `--account agy/<profile>` pins exactly as for claude/codex; `sb agy` alone balances the auto-eligible accounts (see shared quota pools). Routed tasks launch `agy --prompt-interactive "<task>"`.
* Shared quota pools: accounts with the same non-empty `quota_pool` share one allowance. Automatic selection (unpinned `sb agy`, routed tasks) considers only the pool representative, the first configured member; other members are used only when pinned. A representative at or over `usage_exhausted_pct` on any known window is excluded rather than replaced by another member, so selection falls through to other accounts/families, or exits 3 for `sb agy` with no other auto-eligible account. Different pools and unpooled accounts are independent. Unknown usage keeps a representative eligible.
* Tests must inspect child argv/env using fake binaries/executor; only variable names or booleans in proof.

## Status and diagnostics
`sb status [--json|--plain]`: the four Claude/Codex launch accounts plus every configured (or implicit) Antigravity account, login state from genuine `claude auth status --json` and `codex login status`, using each mapped environment. Antigravity status runs no subprocess: `agy` must be on PATH and login is the existence (stat only, never opened) of `<account HOME>/.gemini/antigravity-cli/antigravity-oauth-token`; a present marker can still be an expired sign-in. Bounded timeouts. Never run inference to discover login, and never relay raw command output/errors. Logged-in is NOT a remaining-credit claim.
`sb doctor`: binary PATH presence (claude, codex, agy), env/Keychain presence only, Codex dirs and Antigravity account HOME dirs, actionable fixes. No automatic installation, login, config writes, or secret printing.

## Automatic routing
`sb "<task>"` and `sb run "<task>"`, global flags accepted including `sb --dry-run "fix a flaky test"`. Rules from config select deterministically and print pick + reason. Skip signed-out and cooldown accounts. Without dry-run, launch chosen real tool with task as its prompt. `--dry-run` must neither launch a task nor write cooldown/usage-cache state or create homes; a local decision-log append is the intentional exception unless `--no-log`. Launch accounts: built-in claude/work, claude/personal, codex/work, codex/personal, plus Antigravity accounts from `[[tool_accounts]]` (default agy/personal); sample TOML. Flags > env > user config > built-in defaults; document supported overrides. Config: $XDG_CONFIG_HOME/switchboard/config.toml else ~/.config/switchboard/config.toml. State: $XDG_STATE_HOME/switchboard else ~/.local/state/switchboard. Restrictive atomic state writes. No credential values in state. Local 429 cooldowns must be supported honestly: exec cannot observe child output/exit after replacement, so implement a documented explicit local cooldown-report command or state input rather than pretending automatic child failure tracking. No proxy/supervisor for tasks.

## Optional Classifier
Classifier interface, deterministic rules and optional Jev adapter. Config TOML
`[classifier]` fields: `provider`, `base_url`, `model`, `api_key_env`,
`api_key_file`, `api_key_keychain`. Provider defaults to `typesafe`; its defaults
are https://api.typesafe.ai, jev-1.14.0, TYPESAFE_API_KEY. Provider `ai-gateway`
defaults to https://ai-gateway.vercel.sh/typesafe, typesafe-ai/jev-1.14.0,
AI_GATEWAY_API_KEY. Provider `off` never resolves keys or calls Jev.
Unknown providers, invalid config and unknown TOML keys fail with exit 2.
Overrides `--classifier-provider` / SB_CLASSIFIER_PROVIDER,
`--classifier-base-url` / SB_CLASSIFIER_BASE_URL and `--classifier-model` /
SB_CLASSIFIER_MODEL follow flags > env > config > built-in defaults.

Resolve keys in order: configured env, optional key file (expand `~/`), then on
darwin only optional generic-password Keychain service using
`security find-generic-password -w -s <service>` with a one-second timeout.
Trim whitespace; missing, unreadable or empty sources fall through to the next
source, then rules. Never output keys or subprocess diagnostics. Doctor reports
provider and key presence only (env, readable file, or darwin Keychain lookup
without -w/-g); off needs no key. No automatic writes.

POST `{base_url}/v1/systemone` after trimming trailing slashes, with
Content-Type application/json, Bearer key, configured model and typed Choice/Score
questions/answers. Three-second bound. No key => no HTTP call. Errors, wrong
model, invalid response, ineligible choice or chosen family probability below account_floor
=> deterministic rules. Accept the configured response model or its equivalent
with/without the `typesafe-ai/` prefix. Never echo keys or raw responses.
No Decision fallbacks, providerOptions or OpenAI response emulation.

Choice criteria are contrastive family objects, merged per-field from built-ins
and `[accounts.codex]` / `[accounts.claude]`. Unknown family IDs are allowed but
have no effect until launchable; unknown fields and empty merged what fail.
Codex: implementation/tests/refactors/everyday coding; Claude: design,
architecture/writing/harder reasoning. AGY (Antigravity) is launchable for tasks that explicitly ask for Antigravity/Gemini; Grok is a non-launchable placeholder. Antigravity is reached only when it is the selected family (rule/default, Jev choice, or explicit pin), never as an automatic fallback from Claude/Codex.
Only unique tools from signed-in, not-cooldown launch accounts enter Choice.
Score uses three structured situation objects with examples, object legends,
0..2 score and a valid 0/1/2 probability distribution.

TOML account_floor and difficulty_floor are finite 0..1, defaulting independently
to 0.5. Explicit account_floor wins; otherwise explicitly set legacy confidence
maps to account_floor (including zero). Omitting both now gives 0.5 rather than
the previous implicit 0.8. Choice confidence never gates acceptance. Difficulty
confidence only gates reporting of difficulty, never account selection; missing
or below-floor confidence gives null difficulty. No difficulty routing rule.

`sb classify --json "<task>"` and routing dry-run share routing eligibility,
login and cooldown checks. Both send task text when provider is enabled and a
key is resolved, but never launch, create homes or write cooldown/usage-cache state; they may append the decision log. JSON includes
tool/profile/reason/source/why and nullable probability, confidence, difficulty,
difficulty_confidence. Rules have null family metrics. Valid raw normalized Jev
choice and metrics remain in a nested jev object even on probability fallback.
Plain output: pick, family, source, probability, confidence, reason, why (tabs), then candidate rows. Status, doctor and
usage never call Jev. Tests use fake transports only.
References: https://docs.typesafe.ai/concepts/how-to-build-with-system-one,
https://docs.typesafe.ai/primitives/choice,
https://docs.typesafe.ai/primitives/score,
https://docs.typesafe.ai/patterns/probability-routing and
https://docs.typesafe.ai/patterns/confidence-routing.

## Strictly read-only usage reporting (same v0.1)
`sb usage --json` prints ONE document:
```
{"schema_version":1,"checked_at":"2026-11-11T21:34:00+11:00","accounts":[{"tool":"claude","profile":"work","account":null,"plan":"Ultra","login_ok":null,"weekly_used_pct":null,"weekly_resets_at":null,"five_hour_used_pct":null,"five_hour_resets_at":null,"extra":{},"source":null,"error":null,"checked_at":"2026-11-11T10:34:00Z"}]}
```
Document-level `checked_at` is RFC3339 in Australia/Sydney with the actual offset (embed time/tzdata for portability). Each account row's `checked_at` is RFC3339 UTC for when that row was read or fetched (cache rows use the cache read/observed time; error rows use the attempt time). Entry keys always present; nullable unknowns, NEVER estimates. Percent numeric 0..100, reset strings only if explicitly known with timezone, other buckets in `extra`. `tool` is an extensible string (Chief appends tool grokbot); no closed tool enum for this JSON contract. README documents the stable versioned shape.
Rows, 6 + N_agy (7 with the implicit default): claude work Ultra; claude personal Ultra; codex work Team (~/.codex); codex personal Plus (~/.codex-personal); one agy row per configured Antigravity account in config order (`account` = configured label; `extra.quota_pool` = pool id when set), else agy personal (account "Antigravity"); grok CLI; plain ~/.claude login state. Antigravity rows are account-keyed so routing can use them; the primary windows come from the "Gemini Models" pool (else the first pool with 5h/weekly windows) and every other pool/window (Claude/GPT etc.) is under `extra.pools`. Antigravity uses the built-in print-mode slash command `agy --output-format json --print /usage` (slash expansion, not a model prompt; `--disable-slash-commands` exists to turn it off) under each account's environment, with a 20-second per-row timeout. used_pct = (1 - remaining_fraction) * 100 clamped 0..100; failures, timeouts or no groups leave nulls with a fixed error. Antigravity plans stay null. Plans above are configured labels, not inferred quota values. Account identifier only if safely provided, otherwise null. One adapter per source and per-source timeout; all-source failure => nonzero, partial success => 0 with per-row errors. Return complete JSON even when every source fails.
HARD SAFETY: no prompt/inference calls, no `codex exec`, no `claude -p` or bare text sent to a model, no sign-in changes, no account-status execution. Read-only official status/usage commands only after verifying local help/semantics; no direct access to credential/token file contents, no unofficial authenticated HTTP APIs. Do not automate trust/login dialogs. Safe existing local usage snapshots may be read as cached (timestamp/freshness disclosed); do not expose session transcripts, secrets or prompts. Missing safe source => explicit nulls/error, not guessed values or a risky fallback. Usage reporting cannot call task executor, routing, or classifier; fake-backed tests must prove this, and no tests call live providers.
Read `/home/box/.local/bin/account-status` SOURCE ONLY as a map; NEVER RUN it. Its Codex check sends a model prompt and MUST NOT be copied. It maps Claude work/personal quota to interactive /usage under ~/.local/share/claude-usage/{work,personal}; AGY native --output-format json --print /usage and /credits (only if these are actual built-in commands, not prompts); Grok interactive /usage (do not send if unsupported, ambiguous or requires mutation). Plain Claude is login-only. Prefer explicit nulls over unsafe probing. Codex may use an officially supported read-only status/rate-limit operation or existing local rate_limits records only, never a new turn. No signed-in browser driving, credentials inspection or private API shortcuts.

## Verification / delivery
Tests cover argv/env parity, defaults, failures, output modes, usage-only command allowlist/no task execution, full-row schema (6 + configured Antigravity accounts), timeout/partial/all-failure, cached-only provenance, no secret reflection, routing precedence/eligibility, cooldowns and fake Jev. Run make ci, build Linux, cross-compile `GOOS=darwin GOARCH=arm64 go build ./cmd/sb`. Live coordinator checks: launch-account status (report failures honestly), codex personal -- --version and -- login status; claude work -- --version; dry-run task pick+reason; sb usage --json validated via jq; exact-value and pattern secret scan of all captured outputs, never print matching secret. No inference in verification.

Later work only: sticky sessions, automatic cooldown discovery where safe, CC-Router-inspired quota controls. No proxy.

## Family and account selection update
Rules/defaults prefer families, ignoring legacy profile suffixes. Bare claude/codex/agy
are also accepted; cooldowns and pins require full tool/profile. Preferred family
first, remaining families Claude then Codex. Jev returns a family and account_floor
is its probability floor; difficulty confidence never blocks a passing family.

Within each family use status-probe login (never usage login) and local cooldowns.
Exclude unknown/signed-out login and cooldown. Binding used is max(known weekly,
five-hour); headroom is 100 minus binding used. The binding window has higher
usage; equal usage binds to the sooner known reset. Any known window >=
usage_exhausted_pct (default 95, finite 0..100) sets the exhausted flag; ignore
extra windows. Rank under-cap accounts with headroom > 0, unknown usage (both
windows null), exhausted accounts with headroom > 0, then fully used accounts
(headroom <= 0). Exhausted accounts never beat an under-cap or unknown sibling.

Within the under-cap tier, harvest eligibility requires headroom >=
harvest_min_headroom_pct (default 10, finite 0..100) and the binding window's own
reset known, in the future and within harvest_window_hours (default 6, finite
and >= 0; zero disables harvesting). Prefer the soonest binding reset among
harvest-eligible accounts; otherwise most headroom. Under-cap accounts below the
minimum still rank above unknown usage. Exhausted accounts with remaining quota
prefer most headroom. Fully used accounts prefer the soonest known binding reset,
missing resets last. Ties prefer work before personal. Harvest reasons use
"expiring soon: resets HH:MM ZONE, N% left"; most-headroom reasons use
"most headroom: N% left". Only fall back to another family if none is signed in
outside cooldown. No usable account exits 3.

--account / SB_ACCOUNT / TOML pin are hard pins in that precedence: bypass Jev
and usage selection, fail exit 3 if unavailable. Explicit family commands only
honor the CLI flag or positional profile as pins, as described above.
Normalized quota cache: usage-cache.json beside cooldown state, atomic 0600 with
0700 parent, no secrets/raw blobs/task text. usage_cache_ttl defaults to "60s",
nonnegative duration. Fresh covered rows skip collectors. Fetch missing/expired
eligible rows at two seconds per row; errors/timeouts reuse cache up to TTL+5m,
otherwise null usage. Cache writes best effort on launch only. Dry-run/classify
never write cooldown/usage-cache state or create homes; decision logging is the only
intentional write unless `--no-log`. Task text still goes to configured keyed classifiers.
Usage stays isolated with 6 + N_agy rows and five seconds per row. Antigravity rows use 20 seconds in both usage and selection, and selection fetches them only when Antigravity is the selected family and only for auto-eligible accounts (pool representatives and unpooled accounts). Ties prefer config order.

Classify/dry-run include family/source/probability/confidence, final tool/profile,
account reason, why and existing difficulty/raw Jev metrics. candidates includes
account ID, status login, cooldown, numeric windows/resets, exhausted, headroom,
chosen, and for pooled accounts quota_pool plus skipped (pool member or pool exhausted). Reset reasons use Australia/Sydney named zones. Rules metrics null;
valid losing Jev numbers retained. README documents source limitations.


## Local decisions, feedback and tuning

All launches, including explicit family commands with passthrough argv, plus
classify and dry-runs, append normalized v1 JSONL
at $XDG_DATA_HOME/switchboard/decisions.jsonl (default
~/.local/share/switchboard/decisions.jsonl). Directory 0700, live file 0600.
[log] defaults: enabled=true, max_bytes=5242880, redact_task=false. max_bytes must
be a positive integer; unknown fields/invalid values fail Load, exit 2. --no-log
suppresses decision logging. No SB_NO_LOG override. Status, doctor, usage, schema
and cooldown never append decisions.

Before an append exceeds max_bytes, rename live to decisions.jsonl.1, replacing
the previous generation, then create live 0600. Both files remain 0600. Records
are indivisible, so one oversized line may exceed the limit. No compression.
Concurrent rotation is not coordinated between processes. Append failures use a
fixed stderr diagnostic and never change routing exit status.

Decision fields: schema_version=1, type=decision, unique RFC4122 UUID id, ts in
Australia/Sydney RFC3339 numeric offset (embedded tzdata), dry_run, explicit_family, task,
task_sha256, family, source (jev/rules), probability, confidence, difficulty,
difficulty_confidence, account, reason, override, exit_code, duration_ms.
Metrics are nullable as in classify. SHA-256 is lowercase hex of raw task bytes,
always present; redaction makes task null. override is null or {account,pin:true}.
explicit_family is always present: true for sb claude/codex/agy, false for run/classify;
old records unmarshal as false. Explicit family launches use source="rules".
No argv, keys, environment, tokens, raw HTTP/Keychain, candidates or usage rows.
Explicit launches have an empty task (null when redacted) and hash empty task bytes.
Failed selection attempts have empty family/account and no implicit label.
Successful dry-run/classify JSON includes id only when the log append succeeded.

Log real launches before syscall.Exec, never fork/wait. Exec cannot observe the
replacement CLI's exit/duration; exit_code and duration_ms are always null,
including pre-exec failures. Dry-run/classify also use null for both. An attempted
launch record does not prove execution or success. Logging is the only dry-run
write exception: no homes, cooldown or usage-cache writes.

sb feedback <id|last> requires --ok or --family/--account correction. last is the
most recent decision across retained generations, ignoring feedback. Family is
claude/codex/agy; account is a valid full launch account and also supplies its family.
Corrections must agree; --ok plus conflicting corrections fails exit 2. Missing
or unknown IDs, empty logs and absent labels fail exit 2 with fixed diagnostics.
Feedback appends even with --no-log or [log] enabled=false if the target exists.
Records include schema_version=1, type=feedback, own id/ts, decision_id, labelled
family/account and ok boolean. Same rotation/permissions. Explicit --account,
SB_ACCOUNT and TOML pin decisions contain an equivalent implicit label object
for that exact family/account on task routing; explicit family launches label only
CLI --account or positional profile (work/personal; agy: a configured profile) pins. Unpinned picks do not imply labels.

sb tune reads only live and .1 local JSONL, never providers, classifier or usage.
Missing files produce zeros/no recommendation. Join labels by decision id;
latest feedback replaces previous or implicit labels. A family label defines a
labelled decision. Count all retained decisions and labelled decisions. Report
per predicted family correct/n and accuracy (null when n=0).
For labelled Jev decisions with probability, floors .5/.6/.7/.8/.9 report
coverage=n_at_or_above_floor/n_labelled_jev_with_probability and
accuracy=n_correct_at_or_above_floor/n_at_or_above_floor (null when n=0).
Recommend highest accuracy among floors with >=.80 coverage, ties higher floor.
No qualifying floor means no recommendation. Fewer than 30 family labels sets
too_few_labels=true and an explicit provisional warning. Compute tables anyway;
no automatic configuration changes. Implicit pins may inflate measured accuracy.
Stable tab-separated human/plain output and one JSON document; schema --json
includes these commands and contracts. Labels whose decisions rotated out do not
contribute. Tests use temporary homes/data directories and fakes only.
