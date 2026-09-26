# AGENTS.md

Project instructions for every coding agent working in this repo (Claude Code,
Codex, Kiro, Kimi, Qwen, pi). This is the only instruction file; do not add a
CLAUDE.md.

## Sources of truth

- `README.md`: modules, profiles, configuration, build commands, CI and release flow.
- `docs/BOUNDARIES.md`: which live files `dot` may write. Stay inside it.
- `docs/CEILINGS.md`: accepted design limits and when to revisit each.
  `internal/syncer/ceilings_doc_test.go` checks its code markers.
- `docs/commands/`: generated from the cobra help; never edit by hand.

Facts owned by those files are updated there. The rules below that no other
file states are maintained here.

## Verify

- `make build`, `make test`, `make lint` (README, Development).
- `make docs` after any command help change; CI fails if `docs/commands` drifts.
- Sync changes: prove rsync and Go filter parity with a real-rsync test, as in
  `internal/syncer/excludes_test.go`.

## Workflow

- Branch and PR; never commit to `main` directly.
- Conventional commits, in English.
- Before merge: CI green and a read-only OCR delegation review of the PR head.
- Release: README, CI/CD.
