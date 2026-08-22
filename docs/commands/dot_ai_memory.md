## dot ai memory

Manage shared claude-mem integration for Codex, Kimi, Kiro, and Copilot

### Synopsis

Use one claude-mem store across Codex, Kimi Code, Kiro CLI, and GitHub Copilot CLI.

Codex keeps the plugin's native lifecycle hooks. Kimi, Kiro, and Copilot
receive the same MCP recall server plus a workspace-aware transcript capture
bridge.

### Options

```
  -h, --help   help for memory
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
* [dot ai memory install](dot_ai_memory_install.md)	 - Install and start the cross-CLI claude-mem integration
* [dot ai memory status](dot_ai_memory_status.md)	 - Show claude-mem integration health for all four CLIs

