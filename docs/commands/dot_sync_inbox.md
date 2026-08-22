## dot sync inbox

Inspect and manage the mirror intake staging area

### Synopsis

View what's staged + tracked under .dotfiles/sync/, force a
re-intake of one path, or clear the imports + tombstones manifests
entirely.

  dot sync inbox                  # alias for list
  dot sync inbox list
  dot sync inbox forget <relpath> # next intake re-stages this path
  dot sync inbox clear            # empty imports + tombstones

```
dot sync inbox [flags]
```

### Options

```
  -h, --help   help for inbox
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
* [dot sync inbox clear](dot_sync_inbox_clear.md)	 - Empty imports.manifest and tombstones.log
* [dot sync inbox forget](dot_sync_inbox_forget.md)	 - Drop a path from imports.manifest so the next intake re-stages it
* [dot sync inbox list](dot_sync_inbox_list.md)	 - Show staged run-dirs, imports manifest entries, and tombstones

