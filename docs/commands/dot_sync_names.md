## dot sync names

Maintain workspace filenames (Unicode NFD, trailing whitespace)

```
dot sync names [flags]
```

### Options

```
  -h, --help   help for names
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
* [dot sync names normalize](dot_sync_names_normalize.md)	 - Plan or apply Unicode NFD filename normalization
* [dot sync names trim](dot_sync_names_trim.md)	 - Plan or apply trailing-whitespace filename trims

