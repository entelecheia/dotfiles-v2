## dot sync owner

Show or set which machine may push this profile

### Synopsis

Show or set which machine may push this profile.

Renaming a Mac: run dot sync owner --rename <old> <new> on that Mac. It
rewrites the owner in every profile of this workspace owned by <old> (mirror
and peer alike), then does the same on the peer over ssh unless --local-only.
Only a profile whose current owner is <old> is renamed. The old name stays
as an alias, so the guard and the peer's owner check keep matching while
either Mac still answers to it; a generic name such as "Mac" is not kept.
The coordinator retires its aliases at the first complete peer sync that
finds the peer recording the new owner, once this Mac answers to the new
name; the peer's copies stay until its owner next changes. At equal epochs
the peer fence refuses a peer that passes its own owner guard. When the peer
cannot be reached, the rename runs only on a Mac that still answers to <old>
(or with --local-only). The epoch, targets and baselines are untouched, so no
run plans a deletion. It refuses when this Mac answers to neither name, when
the peer (or the peer target's host) answers to either one, and, once this
Mac no longer answers to <old>, unless it runs the peer scheduler and the
peer, asked then, runs none (without a peer, or a peer that cannot answer,
only --local-only renames it): moving ownership between the Macs is --set or
dot peer handover. --local-only skips these checks; it is the step the
command runs on the peer. A retry after a peer failure goes on to the peer.
--dry-run shows the change without writing anything.

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

