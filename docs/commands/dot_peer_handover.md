## dot peer handover

Hand the coordinator role to the peer (planned switch)

### Synopsis

Planned coordinator switch, run on the CURRENT coordinator with both
machines reachable:

  1. Run one complete peer sync; refuse if it holds anything back.
  2. Set the owner and the next epoch on the peer, then locally.
  3. Remove the local scheduler.
  4. On the peer, set the old baselines aside and run the first sync as an
     additive bootstrap (no baseline means no deletes can be planned, and
     right after step 1 almost nothing transfers).
  5. Install the peer's scheduler.

The takeover replica is not used here: right after a complete run the safest
baseline is no baseline. After the switch, realign the repos on the new
coordinator with `dot peer git realign --apply` (fetch first).

```
dot peer handover [<peer>] [flags]
```

### Options

```
  -h, --help   help for handover
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

