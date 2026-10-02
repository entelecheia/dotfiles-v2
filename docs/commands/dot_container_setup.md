## dot container setup

Install the backend and write the container shim

### Synopsis

Enable the container module in user state, install the backend, and write
the shim, its PATH entry and the state file. Afterwards it is equivalent to
dot apply --module container (plus the shell module for the PATH entry).

On Linux an existing, usable docker is used as is. With no docker or podman,
setup offers to install the distro docker package (--yes accepts) and falls
back to podman when declined or when the docker install fails.

```
dot container setup [flags]
```

### Options

```
  -h, --help   help for setup
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

* [dot container](dot_container.md)	 - Shared container command (apple/container on macOS, docker or podman on Linux)

