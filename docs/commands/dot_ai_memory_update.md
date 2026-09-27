## dot ai memory update

Update claude-mem to the latest marketplace version

### Synopsis

Refresh the thedotmack marketplace checkout, compare the installed
claude-mem version, and update the Claude Code plugin when behind. The
codex plugin cache is reinstalled and its bun runtime re-materialized, so
both CLIs converge on the same version. Idempotent when already current;
the scheduled peer sync runs this check before every sync.

```
dot ai memory update [flags]
```

### Options

```
  -h, --help   help for update
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

* [dot ai memory](dot_ai_memory.md)	 - Manage shared claude-mem integration for Codex, Kimi, Kiro, Copilot, Qwen, and pi

