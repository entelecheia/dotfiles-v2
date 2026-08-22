## dot sync configure

Persist sync profile and scheduler settings

```
dot sync configure [flags]
```

### Options

```
  -h, --help                   help for configure
      --json                   print the refreshed status document
      --max-delete int         maximum deletes per push; 0 uses the default (default -1)
      --owner string           self, none, or a machine name
      --propagate string       comma-separated create,update,delete
      --pull-interval string   pull cadence, or 0 to disable
      --pull-mode string       clean or force
      --push-interval string   push cadence, or 0 to disable
      --push-mode string       clean or force
      --target string          local:path or ssh:user@host:path
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

