# switchboard v0.1 (`sb`)

## Purpose and boundaries
A local-first Go CLI that selects among your own coding CLI logins and launches the genuine tool with exec. No provider traffic proxy, token store, OAuth extraction/refresh implementation, or subscription API emulation. Pluggable provider adapters; honest support documentation. Inspired by paulmeller/oma in spirit only: do not copy code. Do not alter the existing dotfiles launchers.

## CLI contract
Use Kong, `cmd/sb` and `internal/`, colocated tests, gofumpt/goimports. Data to stdout, diagnostics to stderr. Human default, stable `--plain`, one-document `--json`, `--version`, `--no-input`, `--dry-run`, help and NO_COLOR. Validate before acting. Exit 0 success, 1 operation failure, 2 usage/config validation, 3 no eligible account. `sb schema --json` describes the actual command tree and contracts. Linux and macOS, including darwin/arm64. `make ci` = pinned formatting check, vet, tests and build. No secrets in output, errors, schema, config, tests, subprocess diagnostics, or logs.

## Existing launcher parity (read these files before implementing)
`/workspace/dotfiles/bin/claude-as`, `/workspace/dotfiles/bin/codex-as`; `~/.local/bin/claude-as` points to the same launcher. They default to work when no profile is given, consume work/personal if present, forward all remaining argv unchanged, resolve the real executable on PATH, then exec.
* `sb claude [work|personal] [-- args]`: work CLAUDE_OAUTH_TOKEN_1 or personal CLAUDE_OAUTH_TOKEN_2 maps to CLAUDE_CODE_OAUTH_TOKEN; ANTHROPIC_API_KEY must be absent. Missing token fails without printing it. Other env/argv preserved. On macOS ~/.zshrc exports these env vars from Keychain services claude-oauth-work / claude-oauth-personal; doctor may check presence but never output a value. No new direct secret extraction.
* `sb codex [work|personal] [-- args]`: CODEX_HOME is ~/.codex or ~/.codex-personal, mkdir -p if needed, then syscall.Exec. Never inspect auth files. Note existing codex-as currently omits mkdir; this requested improvement is intentional. Explicit launcher selections bypass routing/status checks.
* Tests must inspect child argv/env using fake binaries/executor; only variable names or booleans in proof.

## Status and diagnostics
`sb status [--json|--plain]`: four accounts, login state from genuine `claude auth status --json` and `codex login status`, using each mapped environment. Bounded timeouts. Never run inference to discover login, and never relay raw command output/errors. Logged-in is NOT a remaining-credit claim.
`sb doctor`: binary PATH presence, env/Keychain presence only, Codex dirs, actionable fixes. No automatic installation, login, config writes, or secret printing.

## Automatic routing
`sb "<task>"` and `sb run "<task>"`, global flags accepted including `sb --dry-run "fix a flaky test"`. Rules from config select deterministically and print pick + reason. Skip signed-out and cooldown accounts. Without dry-run, launch chosen real tool with task as its prompt. `--dry-run` must neither launch a task nor write state/create homes. Four default accounts; sample TOML. Flags > env > user config > built-in defaults; document supported overrides. Config: $XDG_CONFIG_HOME/switchboard/config.toml else ~/.config/switchboard/config.toml. State: $XDG_STATE_HOME/switchboard else ~/.local/state/switchboard. Restrictive atomic state writes. No credential values in state. Local 429 cooldowns must be supported honestly: exec cannot observe child output/exit after replacement, so implement a documented explicit local cooldown-report command or state input rather than pretending automatic child failure tracking. No proxy/supervisor for tasks.

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
model, invalid response, ineligible choice or confidence below configured floor
=> deterministic rules. Accept the configured response model or its equivalent
with/without the `typesafe-ai/` prefix. Never echo keys or raw responses.
No Decision fallbacks, providerOptions or OpenAI response emulation.

Routing dry-runs intentionally classify too: both `sb --dry-run "<task>"` and
`sb run --dry-run "<task>"` send task text externally when a classifier key is
configured and provider is not off. They still never launch, create homes or
write state. Print pick, reason and confidence; JSON confidence is 0..1 on Jev
success and null for rules, with reason "Jev classification" on success. Plain
output shows confidence when present. Status, doctor and usage never call Jev.
Tests use only fake transports/httptest and stub Keychain; no live classification.
References: https://docs.typesafe.ai/concepts/system-one,
https://api.typesafe.ai/openapi.json and
https://vercel.com/docs/ai-gateway/sdks-and-apis/typesafe.

## Strictly read-only usage reporting (same v0.1)
`sb usage --json` prints ONE document:
```
{"schema_version":1,"checked_at":"2026-11-11T21:34:00+11:00","accounts":[{"tool":"claude","profile":"work","account":null,"plan":"Ultra","login_ok":null,"weekly_used_pct":null,"weekly_resets_at":null,"five_hour_used_pct":null,"five_hour_resets_at":null,"extra":{},"source":null,"error":null}]}
```
`checked_at` is RFC3339 in Australia/Sydney with the actual offset (embed time/tzdata for portability). Entry keys always present; nullable unknowns, NEVER estimates. Percent numeric 0..100, reset strings only if explicitly known with timezone, other buckets in `extra`. `tool` is an extensible string (Chief appends tool grokbot); no closed tool enum for this JSON contract. README documents the stable versioned shape.
Exactly eight built-in rows: claude work Ultra; claude personal Ultra; codex work Team (~/.codex); codex personal Plus (~/.codex-personal); agy Gemini pool; agy Claude/GPT pool; grok CLI; plain ~/.claude login state. Plans above are configured labels, not inferred quota values. Account identifier only if safely provided, otherwise null. One adapter per source and per-source timeout; all-source failure => nonzero, partial success => 0 with per-row errors. Return complete JSON even when every source fails.
HARD SAFETY: no prompt/inference calls, no `codex exec`, no `claude -p` or bare text sent to a model, no sign-in changes, no account-status execution. Read-only official status/usage commands only after verifying local help/semantics; no direct access to credential/token file contents, no unofficial authenticated HTTP APIs. Do not automate trust/login dialogs. Safe existing local usage snapshots may be read as cached (timestamp/freshness disclosed); do not expose session transcripts, secrets or prompts. Missing safe source => explicit nulls/error, not guessed values or a risky fallback. Usage reporting cannot call task executor, routing, or classifier; fake-backed tests must prove this, and no tests call live providers.
Read `/home/box/.local/bin/account-status` SOURCE ONLY as a map; NEVER RUN it. Its Codex check sends a model prompt and MUST NOT be copied. It maps Claude work/personal quota to interactive /usage under ~/.local/share/claude-usage/{work,personal}; AGY native --output-format json --print /usage and /credits (only if these are actual built-in commands, not prompts); Grok interactive /usage (do not send if unsupported, ambiguous or requires mutation). Plain Claude is login-only. Prefer explicit nulls over unsafe probing. Codex may use an officially supported read-only status/rate-limit operation or existing local rate_limits records only, never a new turn. No signed-in browser driving, credentials inspection or private API shortcuts.

## Verification / delivery
Tests cover argv/env parity, defaults, failures, output modes, usage-only command allowlist/no task execution, all-eight-row schema, timeout/partial/all-failure, cached-only provenance, no secret reflection, routing precedence/eligibility, cooldowns and fake Jev. Run make ci, build Linux, cross-compile `GOOS=darwin GOARCH=arm64 go build ./cmd/sb`. Live coordinator checks: four-account status (report failures honestly), codex personal -- --version and -- login status; claude work -- --version; dry-run task pick+reason; sb usage --json validated via jq; exact-value and pattern secret scan of all captured outputs, never print matching secret. No inference in verification.

Later work only: quota-aware routing, sticky sessions, automatic cooldown discovery where safe, CC-Router-inspired quota controls. No proxy.
