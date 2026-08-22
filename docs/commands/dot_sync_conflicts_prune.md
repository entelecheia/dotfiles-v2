## dot sync conflicts prune

Remove old conflict backups from selected local/remote trees

```
dot sync conflicts prune [flags]
```

### Options

```
      --all              prune every backup regardless of age
  -h, --help             help for prune
      --older-than int   prune backups older than this many days (default 30)
      --remote-only      prune only SSH target backups; preserve local workspace backups
```

### Options inherited from parent commands

```
      --config string        Path to custom config YAML
      --dry-run              Show what would be done without executing
      --filter-mode string   override config filter mode for this run: include or exclude
      --home string          Override home directory (for admin setup of other users)
      --mode string          execution mode for push/pull: manual, clean, or force (default "manual")
      --module strings       Run specific modules only
      --profile string       sync profile (store under <workspace>/.dotfiles/<profile>/); "sync" is the cloud mirror (default "sync")
  -V, --verbose              Show rsync progress output
      --yes                  Unattended mode (skip all prompts)
```

### SEE ALSO

* [dot sync conflicts](dot_sync_conflicts.md)	 - List or prune .sync-conflicts/ backup directories

