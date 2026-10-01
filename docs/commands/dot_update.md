## dot update

Update dot binary to latest version

### Synopsis

Download and install the latest dot release from GitHub.

A Homebrew install is upgraded through the brew that owns it instead: dot runs
brew update when the tap lags the release, then brew upgrade, checks the new
version and reloads the dot LaunchAgents that run it. A pinned formula is left
alone. --check only reports the latest version.

```
dot update [flags]
```

### Options

```
      --check   Only check for updates without installing
  -h, --help    help for update
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

* [dot](dot.md)	 - User environment & workspace management tool

