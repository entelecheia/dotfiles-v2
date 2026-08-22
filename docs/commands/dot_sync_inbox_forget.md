## dot sync inbox forget

Drop a path from imports.manifest so the next intake re-stages it

```
dot sync inbox forget <relpath> [flags]
```

### Options

```
  -h, --help   help for forget
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

* [dot sync inbox](dot_sync_inbox.md)	 - Inspect and manage the mirror intake staging area

