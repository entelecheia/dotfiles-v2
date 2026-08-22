## dot sync target

Show or set the sync target (local mirror dir or SSH remote)

### Synopsis

With no argument, prints the resolved sync target.

With a spec, sets the target in this workspace's local config
(<workspace>/.dotfiles/sync/config.yaml) so it takes effect immediately.
Accepted forms:

  local:~/Dropbox/work       local directory (a cloud client's folder)
  ssh:user@host:~/work       rsync over SSH
  ~/Dropbox/work             bare path — shorthand for local:

Local targets are also recorded in the global user state so future
workspaces inherit them.

```
dot sync target [spec] [flags]
```

### Options

```
  -h, --help   help for target
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

