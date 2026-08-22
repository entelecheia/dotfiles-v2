## dot ai coauthor-guard apply

Apply coauthor guard instruction and Git commit-msg hook

```
dot ai coauthor-guard apply [flags]
```

### Options

```
      --apply-agents       Reapply agents SSOT to live tool targets after updating the instruction
      --force-hooks-path   Replace an existing non-dotfiles core.hooksPath
  -h, --help               help for apply
      --mode string        Guard mode: off, warn, or block (default "warn")
      --persist            Persist modules.git.coauthor_guard for future dot apply runs
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

* [dot ai coauthor-guard](dot_ai_coauthor-guard.md)	 - Warn or block AI-added Co-authored commit trailers

