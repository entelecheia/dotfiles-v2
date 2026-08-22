## dot peer init

Create the peer profile pointing at another machine

### Synopsis

Write <workspace>/.dotfiles/peer/ with a target, secrets opt-in, and the
host-path list.

Use a hostname that survives a network change. Tailscale MagicDNS names
(<machine>.<tailnet>.ts.net) work from any network without a static IP or an
inbound port, which is what a laptop needs.

```
dot peer init [flags]
```

### Options

```
  -h, --help                 help for init
      --host string          ssh destination for the other machine (user@host)
      --remote-path string   workspace path on the peer (default: same as local)
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

