## dot sync conflicts

List or prune .sync-conflicts/ backup directories

### Synopsis

Conflict backups accumulate in both trees: pull backups under the
workspace, push backups under the mirror. For SSH targets, push backups are
listed and pruned on the remote target under <target>/.sync-conflicts; the
peer profile also includes ~/.dot-peer-conflicts from its host-path pass.

  dot sync conflicts                       # alias for list
  dot sync conflicts list
  dot sync conflicts prune                 # remove backups older than 30 days
  dot sync conflicts prune --older-than 7
  dot sync conflicts prune --all           # remove every backup
  dot sync conflicts prune --all --remote-only --profile=peer
                                             # preserve this machine's backups

```
dot sync conflicts [flags]
```

### Options

```
  -h, --help   help for conflicts
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

* [dot sync](dot_sync.md)	 - Sync workspace to a local mirror or SSH remote via rsync
* [dot sync conflicts list](dot_sync_conflicts_list.md)	 - List .sync-conflicts/ backup directories in both trees
* [dot sync conflicts prune](dot_sync_conflicts_prune.md)	 - Remove old conflict backups from selected local/remote trees

