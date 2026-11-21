# Agent instructions

Read `SPEC.md` first. This is a Go CLI, not an API gateway. Keep the implementation compact and testable.

## Safety and scope
- Do not open credential/token/auth files or display env values. You may use real CLIs' read-only status commands and inspect non-secret command help. Usage tests must use fakes.
- Keep user-specific paths and runtime secrets out of repo content.
- No raw subprocess stdout/error forwarding from status/usage, and no tokens in fixtures. Whitelist normalized safe values.

## CLI conventions (read and summarized from upstream)
https://github.com/steipete/agent-scripts/blob/main/skills/create-cli/SKILL.md and its cli-guidelines reference: human-first, script-friendly, standard flags, concise help/examples, stdout data vs stderr diagnostics, stable machine output, validate before side effects, no prompts when --no-input/non-TTY, dry-run truly read-only, honor NO_COLOR/TERM=dumb, bounded calls, actionable errors, no telemetry.
https://gogcli.sh/spec.html: Kong command structs are source of truth; cmd/sb entrypoint and internal/cmd orchestration; separate adapters/config/state/output; schema derives from command tree; flags/config/output are compatibility contracts; pinned tooling in Makefile, goimports + gofumpt, fmt-check without edits, colocated fake/httptest tests, make ci.
Exit codes: 0 success, 1 failure, 2 invalid input/config, 3 no eligible account. Include --version, --json, --plain, --no-input, --dry-run and schema --json. No decorative colors is fine. No broader feature creep.

## Acceptance
Implement the complete SPEC with honest unsupported-source/null reporting where safety prevents live quota access. Run make ci and darwin/arm64 compile. Keep final summary brief: files, tests, remaining limitations. Never claim login status implies usable credits. State whether usage has real read-only sources vs cached/null. Do not claim four accounts without checking.
