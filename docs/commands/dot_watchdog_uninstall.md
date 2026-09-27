## dot watchdog uninstall

Remove the watchdog reaper LaunchAgent (macOS)

### Synopsis

Unload and remove the reaper LaunchAgent. Removing the state directory
and the log is an interactive-only prompt that defaults to No; --yes never
auto-confirms it.

```
dot watchdog uninstall [flags]
```

### Options

```
  -h, --help   help for uninstall
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

* [dot watchdog](dot_watchdog.md)	 - Reap runaway processes and alert on host health

