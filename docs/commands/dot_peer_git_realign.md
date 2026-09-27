## dot peer git realign

Move HEAD and index to the descendant commit the files already match

```
dot peer git realign [--apply] [<repo>...] [flags]
```

### Options

```
      --apply   move HEAD and index (default is a dry-run preview)
  -h, --help    help for realign
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

