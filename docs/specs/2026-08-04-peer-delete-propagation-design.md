# Peer delete propagation with quarantine

Status: implemented on `feat/peer-delete-propagation`
Date: 2026-08-04

Two things were learned while building it and are folded in below: the delete
pass must run before the push, not after (see "Retire tombstones"), and rsync
signals the expected missing source args as a partial transfer (see "Delete on
the peer, into quarantine").

## Problem

`dot peer sync` never propagates deletions. A file removed on one machine
comes back within one sync interval, because the peer still holds a copy and
the pull pass restores it.

Observed 2026-08-03: five files deleted and committed at 22:09:44 were back on
disk at ctime 22:14:29, carrying the peer's original mtimes. The same mechanism
had already produced a divergent duplicate of an inbox item earlier that day,
which was initially misdiagnosed as a bad commit.

This blocks any workflow whose normal course is to remove files (inbox
processing is the concrete case, since its whole job is moving material out of
`inbox/drop/` and `inbox/items/pending/`).

### Why `propagation.delete: true` alone does not fix it

The peer profile already has a `delete` knob and `propagationFlags` already
translates it into `--delete-after --max-delete=N`. Setting it to `true` is a
no-op for this problem, because of pass ordering:

| Pass | Flags | Effect on a locally deleted file |
|---|---|---|
| 1. pull (peer → local) | `--update`, no delete flags | **restores it** |
| 2. push (local → peer) | `--delete-after` | present locally again, so nothing to delete |

`pullArgs` hardcodes `--update` and never consults `cfg.Propagation`; only
`pushArgs` calls `propagationFlags`. Verified empirically: with a file present
on the peer and absent locally, pull recreates it and the subsequent push with
`--delete-after` removes nothing.

The root difficulty is that the pull pass cannot distinguish *deleted here on
purpose* from *created over there and not yet seen*. Both are "absent locally".

## What already exists

Most of the machinery is present and does not need to be built.

| Capability | Where | State |
|---|---|---|
| Deletion decision rules | `internal/syncer/push_plan.go` | Complete and safe. Absent-locally + in baseline + unchanged on target → delete; not in baseline → treated as target-side new and protected; changed after baseline → conflict, not deleted |
| Quarantine of removed files | `pushArgs` already passes `--backup --backup-dir=.sync-conflicts/<ts>/from-workspace` | Works. rsync moves deleted files into the backup dir with content and relative path preserved |
| Retention purge | `PruneConflicts` + `dot sync conflicts prune --older-than` (default 30 days) | Complete |
| Tombstone record type | `Tombstone`, `AppendTombstones`, `LoadTombstones`, per-profile `tombstones.log` | Exists, currently written only by mirror intake |
| Delete cap | `MaxDelete` config, `--max-delete` | Exists |
| Modern rsync on both ends | `RemoteRsyncPath` resolves a 3.x rsync on the peer | Exists |

`push_plan.go` cannot be reused wholesale: it calls `collectPlanInventory` on
the target tree, and an SSH peer cannot be walked locally. Its *rules* are
reused; its inventory step is not.

## Design

Four changes, all scoped to the peer profile.

### 1. Compute tombstones before the pull

```
tombstones = { rel : rel ∈ baseline ∧ rel ∉ localTree }
```

Filters apply to the local walk, so excluded paths never enter the set. This
needs only the local tree and `baseline.manifest`, no remote inventory, which
is what makes it work against an SSH peer.

The set is computed at the start of the run, before the pull mutates the tree.
That ordering is the whole point: after the pull, the evidence is gone.

Paths not in the baseline are not deletions. They are files the peer created
that this machine has not seen, and they are left alone. This is the same rule
`push_plan.go` already applies.

The set is *derived* each run rather than accumulated, so it cannot drift out
of step with the tree. Each newly seen path is also appended to the peer
profile's `tombstones.log` with its baseline fingerprint and a detection
timestamp, using the existing `Tombstone` type and `AppendTombstones`. That log
is an audit trail only; nothing in the algorithm reads it back (step 4).

### 2. Protect tombstoned paths during the pull

The tombstone set is written to a per-run filter file, the same way
`prepareRuntimeFilters` already materializes the exclude and include layers,
and that file is passed to the pull. The pull then cannot restore those paths.
Without this step, every later step is moot.

### 3. Delete on the peer, into quarantine

A dedicated pass after the push:

```
rsync --files-from=<tombstones> --from0 --delete-missing-args \
      --backup --backup-dir=.sync-conflicts/<ts>/from-workspace \
      <local>/ <peer>:<path>/
```

`--delete-missing-args` removes exactly the listed paths that are missing on
the sender, and nothing else. `--from0` (NUL-delimited list) keeps Korean and
space-bearing filenames intact, which this workspace has many of.

Verified empirically. Given a tombstone list of `a/gone.txt`:

| Path on peer | In tombstone list | Result |
|---|---|---|
| `a/gone.txt` | yes | removed, quarantined at `.sync-conflicts/<ts>/from-workspace/a/gone.txt`, content and relative path intact |
| `a/peer-new.txt` | no | untouched |
| `a/shared.txt` | no | untouched |

This is preferred over `--delete-after`, which would delete every peer path
absent locally, including files the peer legitimately created.

Every path in the list is missing from the sender by construction, and rsync
reports absent source args as a partial transfer, exit 23/24. For this pass
that exit code is the success signal, so `IsPartialTransfer` is treated as
success and any other error is returned. This only surfaced once the pass ran
against real rsync; an argv-only test cannot see it.

### 4. Retire tombstones

No explicit baseline surgery is needed. `Push` ends with `RefreshBaseline`,
which for an SSH target walks the *local* tree, so a path that is gone locally
drops out of the baseline on its own and stops being a tombstone from the next
run onward.

That is also why **the delete pass runs after the pull but before the push**.
If it ran after the push, `RefreshBaseline` would already have retired the key;
a delete pass that then failed would leave no tombstone, and the peer's copy
would come back on the next pull. Returning early on failure leaves the
baseline untouched, so the deletion is simply retried next run.

- Peer unreachable → baseline unchanged → tombstone recomputed next run and
  applied when the peer returns.
- Path recreated locally → no longer absent → not a tombstone. No special case,
  and its `tombstones.log` row is dropped on the next run.

**Tombstones do not expire.** An earlier draft gave them a 30-day TTL after
which the deletion would be abandoned, but abandoning it means the peer's copy
propagates back on the next sync, which is precisely the bug being fixed here.
A tombstone therefore survives until the delete pass applies it, however long
the peer stays away. The set stays bounded because it is derived from the
baseline, which is bounded by the tree.

`tombstones.log` is an audit trail, not state the algorithm reads back. It
answers "what was removed from the peer, and when" for a destructive operation
that would otherwise leave no local record.

## Safety

- **Cap.** If the tombstone count exceeds `max_delete`, abort the delete pass,
  report, and leave the tree untouched. Guards against a mount failure or a bad
  filter presenting the whole tree as deleted.
- **Quarantine, not removal.** Nothing is unlinked on the peer; files are moved
  under `.sync-conflicts/` and only removed later by the retention purge.
- **Dry run.** `--dry-run` reports the tombstone set and the would-be
  quarantine paths without touching either machine.
- **Pull is unchanged in kind.** It still never deletes locally. The new filter
  only prevents restoration.

## Defaults

| Setting | Value | Reason |
|---|---|---|
| Quarantine retention | 30 days | Matches the existing `conflicts prune` default. This is the only expiry in the design |
| Tombstone lifetime | until applied | Expiring a tombstone would resurrect the file; see "Retire tombstones" |
| `max_delete` | 100 | Routine inbox clearing is tens of files; a larger set means something is wrong |
| Scope | peer profile only | The mirror profile has a single writer and does not have this problem |
| Deployment | both machines | Each Mac runs its own `com.dotfiles.peer` job with itself as owner; symmetric behavior needs the change on both |

## Testing

- Unit: tombstone computation against a fixture baseline plus tree, covering
  in-baseline-and-absent (tombstone), not-in-baseline (peer-new, protected),
  filtered-out (ignored), and recreated-locally (not a tombstone).
- Unit: pull args carry the protective filter; delete-pass args carry
  `--files-from`, `--from0`, `--delete-missing-args`, and a backup dir.
- Unit: tombstone count over `max_delete` aborts without invoking rsync.
- Integration: two local trees standing in for local and peer. Delete a file
  locally, run a full cycle, assert it stays deleted locally, is gone from the
  peer, is present under the peer's `.sync-conflicts/`, and that a peer-only
  file created in the same cycle survives on both sides.
- Regression: existing peer tests asserting no `--delete*` flags on the pull
  pass must still pass. The pull must remain non-deleting.

## Out of scope

- Mirror (`dot sync`) delete propagation.
- Reconciling the two machines' diverged git histories, which is a separate
  open problem (local ahead 9, peer ahead 17 with uncommitted work).
- Automating the retention purge on a schedule. `dot sync conflicts prune`
  stays manual for now; revisit if quarantine accumulation becomes a nuisance.
