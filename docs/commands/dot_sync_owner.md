## dot sync owner

Show or set which machine may push this profile

### Synopsis

Show or set which machine may push this profile.

Renaming a Mac: dot sync owner --rename <old> <new> rewrites the owner in
every profile of this workspace owned by <old> (mirror and peer alike), then
does the same on the peer over ssh unless --local-only. The old name stays as
an alias, so the guard and the peer's owner check keep matching while either
Mac still answers to it. The epoch, targets and baselines are untouched, so
no run plans a deletion.

Keep the peer target's ssh alias through a rename: the target is part of the
baseline identity (baseline.peer-target), and editing target: in the peer
config resets the baseline. Point the old alias at the new host name in
~/.ssh/config instead.

```
dot sync owner [--rename <old> <new>] [flags]
```

### Options

```
      --clear        remove the ownership restriction
  -h, --help         help for owner
      --local-only   with --rename, leave the peer alone
      --rename       record a machine rename <old> <new> in every profile, here and on the peer
      --set string   set ownership to a specific machine name
      --set-self     claim ownership for this machine
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

