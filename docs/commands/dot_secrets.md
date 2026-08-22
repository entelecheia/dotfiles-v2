## dot secrets

Manage encrypted secrets

### Synopsis

Encrypt, backup, restore, and inspect dot secrets using age.

### Options

```
  -h, --help   help for secrets
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
* [dot secrets backup](dot_secrets_backup.md)	 - Copy encrypted secrets to a destination directory
* [dot secrets init](dot_secrets_init.md)	 - Encrypt SSH key and shell secrets with age
* [dot secrets list](dot_secrets_list.md)	 - List encrypted secrets files
* [dot secrets restore](dot_secrets_restore.md)	 - Decrypt secrets from a source directory
* [dot secrets status](dot_secrets_status.md)	 - Check status of secrets files

