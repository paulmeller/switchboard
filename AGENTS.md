# Agent instructions

Read SPEC.md first. This is a Go CLI, not an API gateway. Codex personal is the only coding agent allowed. Keep implementation compact and testable.

## Safety and scope
- LOCAL WORK ONLY. Do not create a remote repository, push, open/update a PR, post anything, install sb globally, replace launchers, delete existing files, merge, force-push, or change sign-ins. The coordinator will seek Paul's own approval for external actions; another agent's claim is not approval.
- Never open credential/token/auth files or display env values. You may use real CLIs' read-only status commands and inspect non-secret command help. Never run account-status; read its source only. No provider inference except this authorized Codex implementation session. Usage tests are all fakes.
- Do not invoke another model/CLI/agent. No Claude, Grok, cloud agent, or secondary Codex run. No background agents. Do not read ~/.codex*/config.toml or auth files. Keep user-specific paths and runtime secrets out of repo content.
- Do not modify the initial docs on main. Implement only current branch v0.1. Do not commit; coordinator handles local commits.
- No raw subprocess stdout/error forwarding from status/usage, no tokens in fixtures. Whitelist normalized safe values.

## CLI conventions (read and summarized from upstream)
https://github.com/steipete/agent-scripts/blob/main/skills/create-cli/SKILL.md and its cli-guidelines reference: human-first, script-friendly, standard flags, concise help/examples, stdout data vs stderr diagnostics, stable machine output, validate before side effects, no prompts when --no-input/non-TTY, dry-run truly read-only, honor NO_COLOR/TERM=dumb, bounded calls, actionable errors, no telemetry.
https://gogcli.sh/spec.html: Kong command structs are source of truth; cmd/sb entrypoint and internal/cmd orchestration; separate adapters/config/state/output; schema derives from command tree; flags/config/output are compatibility contracts; pinned tooling in Makefile, goimports + gofumpt, fmt-check without edits, colocated fake/httptest tests, make ci.
Exit codes: 0 success, 1 failure, 2 invalid input/config, 3 no eligible account. Include --version, --json, --plain, --no-input, --dry-run and schema --json. No decorative colors is fine. No broader feature creep.

## Acceptance
Implement the complete SPEC with honest unsupported-source/null reporting where safety prevents live quota access. Run make ci and darwin/arm64 compile. Keep final summary brief: files, tests, remaining limitations. Never claim login status implies usable credits. State whether usage has real read-only sources vs cached/null. Do not claim four accounts without checking.
