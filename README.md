# switchboard

[![ci](https://github.com/paulmeller/switchboard/actions/workflows/ci.yml/badge.svg)](https://github.com/paulmeller/switchboard/actions/workflows/ci.yml)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Stripe requests across your AI coding subscriptions.**

If you juggle multiple Claude, Codex, and Antigravity subscriptions, `sb`
stripes requests across them — sending each task to the right CLI and the
account with the most headroom (or quota about to reset), using the genuine
tools directly. It stores no credentials, proxies no traffic, and never
equates a login with available credits. Linux and macOS are supported.

```sh
sb 'fix the flaky parser test'    # pick family + least-used account and run
sb claude                         # balance Claude accounts by headroom
sb codex personal -- --version    # pin codex/personal and pass args through
```

---

## Install

**Homebrew** (macOS / Linux):

```sh
brew install paulmeller/tap/switchboard
```

**With Go (from a local clone):**

```sh
go install ./cmd/sb
```

**From source:**

```sh
git clone https://github.com/paulmeller/switchboard.git
cd switchboard
make ci          # builds ./build/sb (Linux/macOS)
make darwin      # cross-builds darwin/arm64
```

You also need the genuine CLIs you plan to launch (`claude`, `codex`, `agy`)
installed on your `PATH` and signed in through their own flows.

## Quick Start

Check what's installed and visible to `sb`:

```sh
sb doctor
sb status
sb usage
```

Launch a specific tool (auto-selecting the best signed-in account in that
family, or pinning one):

```sh
sb claude
sb claude work -- --model sonnet
sb codex personal
sb agy
```

Or give `sb` a task and let it route to the best family and account:

```sh
sb --dry-run 'refactor the config parser'
sb 'implement the parser'
```

## Profiles & Accounts

Declare your accounts in `$XDG_CONFIG_HOME/switchboard/config.toml` (default
`~/.config/switchboard/config.toml` — see
[config.example.toml](config.example.toml)). Each tool isolates profiles using
its own native mechanism — `sb` never reads or writes credential files:

| Tool | Account ID | How profiles isolate |
|---|---|---|
| `claude` | `claude/<name>` | `config_dir` -> `CLAUDE_CONFIG_DIR`, or `token_env` -> `CLAUDE_CODE_OAUTH_TOKEN` (strips `ANTHROPIC_API_KEY`) |
| `codex` | `codex/<name>` | `home` -> `CODEX_HOME` (defaults to `~/.codex-<name>`) |
| `agy` | `agy/<name>` | `home` -> isolated `HOME` + `XDG_*` dirs (or process `HOME` if omitted) |

```toml
[[tool_accounts]]
id = "claude/client"
label = "Claude (client)"
plan = "Max"
token_env = "CLAUDE_OAUTH_TOKEN_CLIENT"   # or config_dir = "~/.claude-client"

[[tool_accounts]]
id = "codex/client"
label = "Codex (client)"
plan = "Pro"
home = "~/.codex-client"

[[tool_accounts]]
id = "agy/work"
label = "Antigravity (work)"

[[tool_accounts]]
id = "agy/second"
label = "Antigravity (second)"
home = "~/profiles/agy-second"
quota_pool = "shared-pool-id"
```

- **With no `[[tool_accounts]]` for a tool**, `sb` falls back to implicit
  defaults (`claude/{work,personal}` using `CLAUDE_OAUTH_TOKEN_1`/`2`,
  `codex/{work,personal}` using `~/.codex` / `~/.codex-personal`, and
  `agy/personal`). Declaring any entry for a tool replaces its defaults.
- **Custom `claude` profiles** require either `config_dir` (isolated sign-in
  directory) or `token_env` (name of an exported env var).
- **Custom `codex` profiles** accept `home` (defaulting to
  `~/.codex-<profile>`).
- **Signing in:** Run `sb login <tool>/<profile>` (e.g. `sb login claude/client`,
  `sb login codex/client`, `sb login agy/second`) to create the profile's
  isolated directory and launch the genuine CLI's own login flow (`claude auth
  login`, `codex login`, or `agy`). For `claude`, the profile must use
  `config_dir`.
- **Antigravity (`agy`)** uses one implicit `agy/personal` until you declare any
  `agy/...` entries. Accounts sharing a `quota_pool` share one allowance:
  automatic selection only ever considers the first configured member, so `sb`
  never hops to another pooled account for extra capacity.

## Routing, Quota Balancing & Cooldowns

1. **Pins win immediately:** `--account tool/profile` > `SB_ACCOUNT` > TOML
   `pin` (and positional `sb <tool> <profile>`) bypasses ranking and targets
   that exact account.
2. **Family selection (for task prompts):** Deterministic substring `[[rules]]`
   and `default` run first, or an optional external classifier (`[classifier]`,
   supporting `typesafe`, `ai-gateway`, or `off`) if a key is configured.
   > **Note:** When a classifier key is configured, `sb <task>`, `sb run`,
   > `sb --dry-run <task>`, and `sb classify` send task text to the configured
   > classifier endpoint. `status`, `doctor`, and `usage` never do.
3. **Account ranking:** Within the selected family, signed-in accounts outside
   cooldown are ranked by remaining headroom:
   - Under-cap accounts with expiring quota (`harvest_window_hours`) come first
     so unused capacity isn't wasted.
   - Next come under-cap accounts ordered by most remaining headroom, then
     unknown-usage accounts, then exhausted accounts.
4. **Cooldowns:** `sb` waits for the child process and forwards its exit code
   without inspecting tool output. Report rate limits explicitly:
   ```sh
   sb cooldown codex/work --duration 1h
   ```

## Decision Log & Calibration

Task launches, explicit launches, dry-runs, and `sb classify` append a local
JSONL record to `$XDG_DATA_HOME/switchboard/decisions.jsonl` (mode `0600`,
rotates at `[log] max_bytes`). Pass `--no-log` or set `redact_task = true` to
omit task text while keeping `task_sha256`.

Label past decisions and check calibration locally:

```sh
sb feedback last --ok
sb feedback last --family claude
sb tune
```

## Machine Output & Exit Codes

Every reporting command supports `--json` (one JSON document on stdout) and
`--plain` (stable tab-separated fields), honors `NO_COLOR` / `TERM=dumb`, and
never prompts (`--no-input` is accepted). Run `sb schema --json` for the full
command and JSON contract.

- `0` — success (including partial `usage` success)
- `1` — operational or all-source failure
- `2` — invalid arguments or config
- `3` — no eligible signed-in account

## Development

```sh
make ci          # format check, vet, tests, and local build
make darwin      # cross-compile darwin/arm64
```

See [SPEC.md](SPEC.md) for the full technical contract and safety invariants.

## License

MIT — see [LICENSE](LICENSE).
