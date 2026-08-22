## dot sync shared remove

Remove one or more paths from the manual shared-excludes list

```
dot sync shared remove <path>... [flags]
```

### Options

```
  -h, --help   help for remove
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

* [dot sync shared](dot_sync_shared.md)	 - Manage manual shared-folder exclusions

