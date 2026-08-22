## dot sync pull

Restore/update baseline-tracked mirror payloads into the workspace

### Synopsis

Pull applies mirror-side changes only for paths listed in
.dotfiles/sync/baseline.manifest. Baseline is expected to be tracked in
Git, so a second machine can git pull the index and then restore binary
payloads from the cloud mirror.

Files absent from baseline are not copied into the workspace by pull; run
intake to stage new mirror-origin files under inbox/gdrive/<ts>/ for manual
review. If local and mirror both changed a baseline-tracked file, manual mode
asks before applying, clean mode aborts, and force mode overwrites local after
backing up the local version into .sync-conflicts/<ts>/from-workspace/.

```
dot sync pull [flags]
```

### Options

```
  -h, --help     help for pull
      --strict   force sha256 fingerprints for every baseline entry (slower; catches content changes that preserve size+mtime)
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

