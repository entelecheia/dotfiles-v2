# AGENTS.md

Project instructions for every coding agent working in this repo (Claude Code,
Codex, Kiro, Kimi, Qwen, pi). This is the only instruction file; do not add a
CLAUDE.md.

## Sources of truth

- `README.md`: modules, profiles, configuration, CI and release flow.
- `docs/BOUNDARIES.md`: which live files `dot` may write. Stay inside it.
- `docs/CEILINGS.md`: sync scale limits.
- `docs/commands/`: generated from the cobra help; never edit by hand.

Update those files, not this one, when commands or policies change.

## Verify

- `make build` (binary in `bin/dot`), `make test`, `make lint`.
- `make docs` after any command help change; CI fails if `docs/commands` drifts.
- Sync changes: prove rsync and Go filter parity with a real-rsync test, as in
  `internal/syncer/excludes_test.go`.

## Workflow

- Branch and PR; never commit to `main` directly.
- Conventional commits, in English.
- Before merge: CI green and a read-only OCR delegation review of the PR head.
- Release by pushing a `v*` tag; the Release workflow runs after Test passes.
