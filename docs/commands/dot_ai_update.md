## dot ai update

Update AI CLIs, plugins, marketplaces, and skills

### Synopsis

Bring managed AI tooling current in one pass.

Phases run in a fixed order (claude, codex, copilot, gemini, kimi, kiro, cursor, skills) and are
partial-failure tolerant: one tool failing never aborts the rest. Missing
binaries are skipped, not errors.

Skills are delegated to 'maru skills update/sync' — dot never writes under a
tool skill root (see docs/BOUNDARIES.md). Plugin updates take effect after a
Claude Code restart.

```
dot ai update [flags]
```

### Options

```
      --check          Report available updates without changing anything
  -h, --help           help for update
      --json           Emit machine-readable JSON
      --tool strings   Limit to specific tools (claude,codex,copilot,gemini,kimi,kiro,cursor,skills)
```

### Options inherited from parent commands

```
      --config string    Path to custom config YAML
      --dry-run          Show what would be done without executing
      --home string      Override home directory (for admin setup of other users)
      --module strings   Run specific modules only
      --profile string   Profile name (minimal, full, server)
      --yes              Unattended mode (skip all prompts)
```

### SEE ALSO

* [dot ai](dot_ai.md)	 - AI CLI/config helpers and settings backup/restore

