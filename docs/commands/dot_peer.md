## dot peer

Sync the workspace directly to another machine over SSH

### Synopsis

Machine-to-machine workspace sync, so either Mac can continue the same work.

This is a sibling of `dot sync`, not a replacement. They answer different
questions:

  dot sync   workspace -> cloud mirror. One writer. Secrets excluded.
             For reading the workspace from a phone or another device.
  dot peer   workspace <-> another machine over ssh. Both directions.
             Secrets included. Submodule working trees included, because
             uncommitted work inside a submodule is exactly what Git has
             not seen and what a second machine still needs.

Routine one-sided changes transfer directly. A path changed differently on
both machines is resolved by the configured coordinator, and the losing peer
payload is quarantined once under .sync-conflicts/ when it exists.

With propagation.delete on, a file removed here is removed on the peer too,
into that same quarantine, and "dot sync conflicts prune" expires it later.
Only paths recorded in the baseline are eligible: a path the peer created and
this machine has never seen is not a deletion and is left alone. The set is
capped by max_delete, so a failed mount cannot present the whole tree as
deleted. With propagation.delete off, a file removed here simply stops being
sent and the peer keeps its copy.

A peer that is offline is not an error: the scheduled run probes reachability
first and exits cleanly when the other machine is away.

```
dot peer [flags]
```

### Options

```
  -h, --help   help for peer
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

* [dot](dot.md)	 - User environment & workspace management tool
* [dot peer diff](dot_peer_diff.md)	 - List paths where this machine and the peer disagree
* [dot peer doctor](dot_peer_doctor.md)	 - Check that a peer sync would work before running one
* [dot peer home-paths](dot_peer_home-paths.md)	 - Read or replace the peer host-path allowlist
* [dot peer init](dot_peer_init.md)	 - Create the peer profile pointing at another machine
* [dot peer setup](dot_peer_setup.md)	 - Install or remove the periodic peer sync job
* [dot peer status](dot_peer_status.md)	 - Show local peer profile and scheduler status
* [dot peer sync](dot_peer_sync.md)	 - Exchange workspace and host paths with the peer (both directions)

