## dot ai

AI CLI/config helpers and settings backup/restore

### Synopsis

Manage portable AI assistant configuration.

The ai module writes shell/config helper files. It does not install Claude,
Codex, Antigravity, or ChatGPT apps; use 'dot apps install' for Homebrew casks.

### Options

```
  -h, --help   help for ai
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

* [dot](dot.md)	 - User environment & workspace management tool
* [dot ai agents](dot_ai_agents.md)	 - Manage the shared AI agents instruction SSOT
* [dot ai audit](dot_ai_audit.md)	 - Inspect append-only dot ai audit events
* [dot ai auth](dot_ai_auth.md)	 - Inspect and refresh OAuth credentials for MCP servers
* [dot ai backup](dot_ai_backup.md)	 - Create a versioned AI settings snapshot
* [dot ai coauthor-guard](dot_ai_coauthor-guard.md)	 - Warn or block AI-added Co-authored commit trailers
* [dot ai export](dot_ai_export.md)	 - Export AI settings to a portable tar.gz archive
* [dot ai hud](dot_ai_hud.md)	 - Manage dot-native Claude Code and Codex HUD status lines
* [dot ai import](dot_ai_import.md)	 - Import AI settings from a portable tar.gz archive
* [dot ai list](dot_ai_list.md)	 - List AI helpers, detected CLIs, and managed paths
* [dot ai memory](dot_ai_memory.md)	 - Manage shared claude-mem integration for Codex, Kimi, Kiro, and Copilot
* [dot ai prune](dot_ai_prune.md)	 - Delete older AI config snapshots, keeping the newest N
* [dot ai restore](dot_ai_restore.md)	 - Restore AI settings from a versioned snapshot
* [dot ai skills](dot_ai_skills.md)	 - Diagnose AI Markdown skills (read-only; the Maru app manages runtime symlinks)
* [dot ai status](dot_ai_status.md)	 - Show AI settings live/backup status
* [dot ai update](dot_ai_update.md)	 - Update AI CLIs, plugins, marketplaces, and skills

