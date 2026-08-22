## dot sync init

Initialize <workspace>/.dotfiles/sync/ from current state

### Synopsis

One-time onboarding for the per-workspace store. Creates
<workspace>/.dotfiles/sync/ with config.yaml, include.txt, exclude.txt,
ignore.txt, manifests, log dir; appends '/.dotfiles/' to <workspace>/.gitignore
so the store is never committed; and creates <workspace>/inbox/gdrive/ if
missing.

Idempotent — re-running on a populated store leaves operator edits intact and
just heals any missing pieces.

```
dot sync init [flags]
```

### Options

```
  -h, --help   help for init
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

