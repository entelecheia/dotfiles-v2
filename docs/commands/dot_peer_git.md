## dot peer git

Realign HEAD and index with the files peer sync delivered

### Synopsis

Peer sync moves files, not git state: HEAD, index and refs stay behind on
the machine that did not make the commits. After a switch, the newly active
Mac realigns instead of pulling: each repo's HEAD and index move forward to
the descendant commit its files already match, through git's compare-and-swap
ref update. The worktree is never written by git, uncommitted modifications
survive, and untracked files never block.

Repos with a lock, an operation in progress, unmerged entries or staged
changes are skipped and reported. The commands never fetch; run git fetch
first when fresh upstream state is wanted.

```
dot peer git [flags]
```

### Options

```
  -h, --help   help for git
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
* [dot peer git realign](dot_peer_git_realign.md)	 - Move HEAD and index to the descendant commit the files already match
* [dot peer git status](dot_peer_git_status.md)	 - Classify every workspace repo against its recorded commit

