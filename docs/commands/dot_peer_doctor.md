## dot peer doctor

Check that a peer sync would work before running one

### Synopsis

Probe everything that silently breaks a peer transfer.

Checks, and why each exists:
  reachability   an offline peer must be a clean no-op, not a failure
  remote rsync   macOS 26 ships openrsync, which cannot receive -aHAX from a
                 3.x client — and --dry-run never surfaces it, because a dry
                 run ships no file data
  clock skew     "newer wins" is only meaningful if the clocks agree
  disk headroom  the receiving side has to hold the payload
  keychain       tokens there cannot be transferred, and cannot even be
                 verified over ssh — a reminder, not a failure

```
dot peer doctor [flags]
```

### Options

```
  -h, --help   help for doctor
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

