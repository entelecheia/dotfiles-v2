## dot peer sync

Exchange workspace and host paths with the peer (both directions)

### Synopsis

Build one baseline-aware plan, accept one-sided peer changes, then
publish coordinator changes. Routine updates are backup-free; only simultaneous
conflicts and propagated deletes create quarantine copies.

When propagation.delete is on, baseline-proven deletes can flow in either
direction. Unknown peer-created paths are pulled and never classified as local
deletions. The common baseline advances only after the complete workspace and
host-path transaction succeeds.

Exits 0 when the peer is unreachable. That is what makes this safe to schedule
on a laptop.

```
dot peer sync [flags]
```

### Options

```
  -h, --help        help for sync
      --pull-only   receive peer changes without pushing
      --push-only   send local changes without pulling first
      --skip-home   workspace only; skip the host-path pass
```

### Options inherited from parent commands

```
      --config string    Path to custom config YAML
      --dry-run          Show what would be done without executing
      --home string      Override home directory (for admin setup of other users)
      --module strings   Run specific modules only
      --profile string   Profile name (minimal, full, server)
      --yes              Unattended mode (skip all prompts)
```

### SEE ALSO

* [dot peer](dot_peer.md)	 - Sync the workspace directly to another machine over SSH

