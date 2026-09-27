## dot ai run

Admit one repository heavyweight command after healthy recovery

### Synopsis

Run a command under the repository resource admission slot. Health is sampled
without launching work; unknown telemetry or uncovered heavy jobs defer it.
The child receives CARGO_BUILD_JOBS=2, RUST_TEST_THREADS=2, and GOMAXPROCS=2.
Pass tool-specific flags (Go -p 2 -parallel 2; browser E2E workers=1) yourself.
Unwrapped commands are detected conservatively, not automatically controlled.

```
dot ai run [--wait 6m] -- COMMAND [ARGS...] [flags]
```

### Options

```
  -h, --help             help for run
      --project string   Git checkout whose worktrees share the slot (defaults to current directory)
      --wait duration    Bounded time to collect healthy recovery samples (0 fails promptly) (default 6m0s)
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

* [dot ai](dot_ai.md)	 - AI CLI/config helpers and settings backup/restore

