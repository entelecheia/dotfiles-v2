# Accepted ceilings

This document records deliberate limits in dotfiles-v2 that are acceptable at
today's operating scale. Each section names the code boundary, why the limit is
accepted, and the concrete event that would require a different design. There
are five required ceilings for DEBT-06 and two additional ceilings minted by
Phase 07's schema-version and per-profile scheduler work.

## Process-wide manifest serialization

`internal/syncer/manifest.go` uses the process-wide `manifestMu` at the four
rewrite-style save sites: `SaveBaselineManifest`, `SaveImportsManifest`,
`AppendTombstones`, and `RewriteTombstones`. It does not use one lock per
manifest file, so independent manifest writes in one process serialize.

This costs nothing while `dot` is effectively single-threaded. Replace it with
per-manifest coordination if `dot` begins concurrent manifest writes.

## Guard hooks are not a sandbox

`internal/cli/guard_cmd.go` describes `dot guard freeze`: it denies editor-tool
writes outside the selected directory, but cannot stop shell writes such as
`sed` or `tee`. `internal/guard/careful.go` also deliberately splits shell
commands with a heuristic rather than parsing Bash.

The guard is a speed bump, not a security sandbox; parsing every Bash write
target is a losing arms race. Replace this limit only when the product adopts a
real sandbox or an enforcement point that covers shell execution.

## Peer first-contact trust on first use

`internal/syncer/rsyncbin.go` connects with
`StrictHostKeyChecking=accept-new` while retaining `BatchMode=yes`.
`internal/syncer/peer_commands.go` also excludes `known_hosts` from peer sync,
because it is per-machine trust state and merging it is meaningless.

Trust on first contact is standard for this personal fleet. A stricter policy
would prompt, and a prompt in a batch-mode scheduled run is unusable. Replace
it if the fleet gains managed host-key distribution or an interactive trust
enrollment flow that works before scheduling.

## Whole-file manifest writes

`internal/syncer/manifest.go` builds complete baseline and imports manifests in
memory and commits them with `atomicWrite`; it does not stream records to disk.

The simpler all-at-once write is not worth changing below six figures of files.
The named upgrade trigger is [CONC-02](../.planning/REQUIREMENTS.md): replace
it with an incremental index or directory-mtime pruning when the workspace tree
reaches six figures of files.

## Preview cannot prove an openrsync transfer

`internal/syncer/rsyncbin.go` documents that `dot sync push --dry-run` never
sends file data. It therefore cannot expose the openrsync protocol failure that
a real `-aHAX` transfer can hit, and `RemoteRsyncPath` probes the peer before
the transfer instead.

This is structurally different from ordinary preview limits: no preview output
can predict a protocol path it intentionally does not execute. Replace the
pre-transfer probe only if rsync provides a non-mutating protocol negotiation
that validates the same data-transfer capability.

## Newer state schema field shapes

`internal/config/state.go` peeks `schema_version`, warns that a file came from
a newer `dot`, names `dot update`, and returns the decode error when an unknown
newer field shape is incompatible with this binary. The binary must not pretend
to understand a newer state document whose fields no longer decode into its
current types.

This ceiling protects data rather than guessing at an unsafe migration. Replace
it only when the state format supplies a backwards-compatible representation or
a versioned migration that this binary can prove is lossless.

## Stale per-profile scheduler units

Scheduler artifacts are resolved once, per profile, at the config resolution
point, and `internal/syncer/sync_cmd_ops.go` reads that resolved layout off the
Config, so a unit installed for a non-default profile now lands at a
per-profile path. A
unit written by a binary from before that change sits at the default path
instead, and `dot` does not remove it: it can keep firing beside the corrected
unit, against the same tree.

Cleanup is withheld rather than attempted because it cannot be done safely yet.
Removing the stale unit means enumerating profile names that no longer appear
in any config, and a scheduler subcommand that deletes service-manager
artifacts has to carry its own dry-run and idempotence contract before it can
be trusted to guess at them. A preview that removes a unit is the failure this
would introduce.

Replace this limit when scheduler cleanup gains that contract, or when the
scheduler records the profile it was installed for so historical units can be
identified without enumeration. Until then, operators upgrading across the
per-profile change remove the stale default-path unit by hand.

## Local home quarantine is not listed by `dot sync conflicts`

The tracked host-path pass (`internal/syncer/peer_home_tracked.go`)
quarantines on both sides: the receiving machine's
`~/.dot-peer-conflicts/<ts>/from-peer/`. `dot sync conflicts` and its prune
cover the remote home root over ssh (the "remote home" tree in
`internal/syncer/sync_conflict_ops.go`), but the LOCAL home root is not a
conflict tree: `ConflictTrees` walks only the workspace and the mirror, and
`ListConflicts` only understands a `.sync-conflicts` directory name.

Accepting this is deliberate for now: the local copies are the coordinator's
own pre-delete payloads, small and few at current operating scale, and making
the conflicts walker understand a second root name is a wider change than the
feature needs. Replace this limit when the conflicts listing learns
named-root trees; until then, prune `~/.dot-peer-conflicts` by hand.

## Unplanned peer switch window

`internal/syncer/peer_handover.go` implements `dot peer takeover`: the Mac
becoming active installs the replica the last coordinator pushed after its
last complete run and adopts ownership with a higher epoch. Anything the old
coordinator changed after that run — unpushed commits and uncommitted work —
never reaches the new coordinator's baseline. When the old Mac returns, the
first run reconciles precisely and the active Mac's simultaneous edits win,
but the pre-replica-gap edits are simply absent.

This is accepted: the alternative (machine-to-machine Git transport,
checkpoints, or `refs/peer/*`) was rejected by the owner on 2026-09-27 for
conflict and loss risk. The window is one sync interval, and the workspace
rule of pushing on every commit keeps it small. Replace this limit only if a
safe machine-to-machine state channel is ever designed.

## host_merge read-merge-write race

`internal/syncer/peer_host_merge.go` (`mergePeerHostFiles`) reads both copies
of a `host_merge` file, merges them and writes the result on both machines,
pushing the merged bytes from a private copy. The additive pass of every
run excludes these files by config, and the create-only pass that copies a
regular file on one Mac only does not replace a copy (a one-way run holds a
file on both). An app that rewrites the file between the last
check and the write (Claude Code saving `~/.claude.json` while `dot peer
sync` runs, often from inside a Claude session) loses that rewrite. A
running app that later saves a stale copy changes only its own Mac, and the
next two-way run's merge brings the missing entries back there.

No lock exists that Claude Code honors for `~/.claude.json`. The merge
re-reads both copies right before it writes, then re-checks the local
file's size and mtime and reads the peer's copy again just before the
write; a file saved in between is decided again from the new copy (three
attempts, then the run stops before the additive pass). What is left is
the local write itself on this Mac, and on the peer the time from that
second read to the push (one ssh connection and a one-file rsync). The
create-only pass leaves a copy that appeared during the run alone except in
the milliseconds between rsync's existence check and its rename of the
received file over the path.
Replace this if the app gains a lock or an atomic update protocol, or if
lost entries are reported in practice.

## Owner aliases outside the coordinator's peer run

`internal/syncer/sync_store_ops.go` (`RenameOwner`, `RetireOwnerAliases`)
keeps a renamed owner's earlier names in `owner_aliases` so a Mac still
answering to the old name keeps its role mid-rename. Only the Mac being
renamed records them (the other Mac's migration step records none), and only
the coordinator retires them, at a complete peer sync. Where that run does
not reach (a workspace with no peer profile, or the mirror owner when
another Mac coordinates the peer) they stay until the owner next changes, and
after an offline rename they stay until the other Mac runs its printed
`--local-only` step (each peer sync past the fence says so), so
a Mac that later answers to a retired name (a reinstall that comes back as
the default `<Name>s-MacBook-Pro`) passes the mirror owner guard there.

A reused default name on a reinstalled Mac of the same user is the only
trigger. Replace this if mirror-only workspaces gain a point where both
Macs are known to record the new owner, or if a reused name is seen in
practice.

## Rescue reads the newest 50 upstream commits

`internal/syncer/gitrescue.go` (`rescueChainLimit`, `bestOnChain`) scores
only the newest 50 commits on the upstream or default branch's first-parent
chain when it looks for a `--rescue` target. A branch whose files match a
commit older than that stays diverged or unclassified, with the suggestion
to rebase or merge by hand; nothing moves. Every commit read costs a content
comparison, and a peer sync delivers recent work, so the match is near the
tip.

Replace this if a real workspace reports a rescue target further back, by
raising the cap or bisecting the chain by content.

## Replica bootstrap trust

A takeover validates the pushed replica (`<workspace>/.dotfiles/peer/replica/`
with `meta.yaml`) by per-file sha256, a reverse-target check against this
profile, and a generation counter (`<workspace>/.dotfiles/peer/replica-generation`)
that must not go backwards. The FIRST takeover on a store that has never
recorded a generation accepts whatever the replica says: there is no local
evidence to compare against, and the channel that wrote the replica is the
same ssh trust the sync itself runs on.

Accepting a first-seen replica is deliberate: rejecting it would make every
first unplanned switch impossible, which is the exact moment takeover exists
for. The generation counter closes the stale-replica hole from the second
switch onward. Replace this limit if the replica gains a signature or a
second, independent provenance channel.
