## dot profile backup

Create a new versioned snapshot of this host's profile

```
dot profile backup [flags]
```

### Options

```
  -h, --help              help for backup
      --include-secrets   Copy ~/.ssh/age_key* into the snapshot
      --tag string        Human-friendly label stored in meta.yaml
      --to string         Backup root (overrides configured BackupRoot)
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

