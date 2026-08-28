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

`internal/syncer/sync_cmd_ops.go` resolves scheduler artifacts per profile, so
a unit installed for a non-default profile now lands at a per-profile path. A
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
