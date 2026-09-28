## dot peer setup

Install or remove the periodic peer sync job

### Synopsis

Schedule dot peer sync.

An unreachable peer exits 0, so a laptop that is away simply produces quiet
no-op runs rather than failures. That is why this can be scheduled at all.

Pick an interval in minutes, not seconds: the payload is large and each run
walks the whole tree.

Role hooks: the machine without the coordinator role must run no jobs that
write the workspace. List them in .dotfiles/peer/config.yaml:

  hooks:
    on_deactivate:
      - launchd-bootout com.maru.job.*
      - app-quit Maru
    on_activate:
      - launchd-bootstrap com.maru.job.*
      - app-open Maru

on_activate runs after this command installs the scheduler (also the step a
handover runs on the new coordinator, and the one a takeover names next);
on_deactivate runs after --off, on the old coordinator in a handover, and on
a machine that demotes itself at the fence. --dry-run lists what each would
do. Results are printed and appended to the peer log; a failed hook never
stops the command or a sync.

```
dot peer setup [flags]
```

### Options

```
  -h, --help                help for setup
      --interval duration   how often to sync with the peer (default 15m0s)
      --off                 remove the scheduled job
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

