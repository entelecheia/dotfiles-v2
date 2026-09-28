## dot peer git realign

Move HEAD and index to the descendant commit the files already match

### Synopsis

Move each repo's HEAD and index forward to the descendant commit its files
already match. The default is a preview; --apply moves.

A repo with no such descendant is reported as no-match with a class and the
next step:
  at-tip            at the upstream tip; only uncommitted changes differ
  ahead-unpushed    local-only commits the upstream lacks; push them
  diverged          local-only commits and upstream commits; the files match
                    an upstream commit
  branch-mismatch   HEAD is on another branch, the files match the default
                    branch
A leftover REBASE_HEAD with no rebase in progress is skipped as
stale-rebase-head with the command that clears it.

--rescue also moves diverged and branch-mismatch repos: HEAD's commits are
kept on rescue/<yymmdd>-<branch>, pushed to the remote (--no-push keeps it
local), then HEAD and the index move to the matching commit, on the default
branch for a branch mismatch. The worktree is never written; every move
prints its undo command.

```
dot peer git realign [--apply] [--rescue [--no-push]] [<repo>...] [flags]
```

### Options

```
      --apply     move HEAD and index (default is a dry-run preview)
  -h, --help      help for realign
      --no-push   with --rescue, keep rescue branches local
      --rescue    also move diverged and branch-mismatch repos, keeping HEAD on a pushed rescue/<date>-<branch> branch
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

* [dot peer git](dot_peer_git.md)	 - Realign HEAD and index with the files peer sync delivered

