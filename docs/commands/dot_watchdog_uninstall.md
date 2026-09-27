## dot watchdog uninstall

Remove the watchdog reaper agent, WARP heal daemon, and monit supervision (macOS)

### Synopsis

Unload and remove the reaper LaunchAgent, the monit agent and monitrc,
and, when installed, the root WARP heal LaunchDaemon. Restoring the power
settings saved by setup --headless, removing the root Screen Sharing heal
helper and its sudoers grant, and removing the state directory and logs are
interactive-only prompts that default to No; --yes never auto-confirms them.

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

