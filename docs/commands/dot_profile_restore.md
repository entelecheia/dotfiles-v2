## dot profile restore

Apply a profile snapshot (defaults to the latest) back to this host

```
dot profile restore [flags]
```

### Options

```
      --from string       Backup root (overrides configured BackupRoot)
  -h, --help              help for restore
      --host string       Source hostname to restore from (default: this host)
      --include-secrets   Restore ~/.ssh/age_key* from the snapshot if present
      --no-state          Skip copying config.yaml back to ~/.config/dotfiles/
      --version string    Specific version to restore (default: latest)
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

* [dot profile](dot_profile.md)	 - Snapshot and restore dot profile state (config, app lists, secrets)

