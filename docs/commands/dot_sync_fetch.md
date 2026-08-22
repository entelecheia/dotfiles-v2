## dot sync fetch

Restore specific files or folders from the target on demand

### Synopsis

Fetch pulls the named files or directories (workspace-relative paths)
from the sync target into the workspace — the on-demand entry point when a
specific file, folder, program, or event needs the binaries backing a path
without running a full pull. Other tools can shell out to it:

  dot sync fetch projects/oda/koica-tiu/06-proposal
  dot sync fetch admin/scan.pdf research/data --dry-run

Safety: newer local files are never overwritten (--update), overwrites are
backed up under .sync-conflicts/, nothing is deleted, and the exclude layers
still apply — .git and non-allowed secrets can never be imported. Paths
missing on the target are reported and skipped.

```
dot sync fetch <path>... [flags]
```

### Options

```
  -h, --help   help for fetch
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

