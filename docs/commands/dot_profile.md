## dot profile

Snapshot and restore dot profile state (config, app lists, secrets)

### Synopsis

Manage per-host profile snapshots under <backup-root>/profiles/<hostname>/<version>/.

Each snapshot captures:
  - config.yaml          → ~/.config/dotfiles/config.yaml
  - apps/install.yaml    → install list (casks + casks_extra)
  - apps/backup.yaml     → backup list + backup root
  - meta.yaml            → timestamp, tag, hostname, user
  - secrets/             → optional copy of ~/.ssh/age_key* (--include-secrets)

The shared backup root is resolved via --to/--from, the user state
(BackupRoot), an auto-detected Drive "secrets" folder, or a local default.

### Options

```
  -h, --help   help for profile
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
* [dot profile backup](dot_profile_backup.md)	 - Create a new versioned snapshot of this host's profile
* [dot profile list](dot_profile_list.md)	 - List available profile snapshots for this host
* [dot profile prune](dot_profile_prune.md)	 - Delete older snapshots, keeping the newest N
* [dot profile restore](dot_profile_restore.md)	 - Apply a profile snapshot (defaults to the latest) back to this host
* [dot profile root](dot_profile_root.md)	 - Show or set the shared backup root for profiles and app-settings

