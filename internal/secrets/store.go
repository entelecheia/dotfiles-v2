// Package secrets owns the encrypted secrets store: where the archives
// live, which plaintext file each one maps to, and the age machinery that
// moves bytes between the two.
//
// Every entry point takes a per-command Options struct and returns a typed
// result; progress crosses back as Event values. No user-visible string is
// produced here — formatting is the command layer's job — and no decrypted
// secret material, identity byte, or passphrase is ever written to an
// output sink.
package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

// StoreDirRel is the encrypted store's path relative to a user home.
// Exported for callers that resolve it against a session home of their own
// (a --home override) rather than through StorePath.
const StoreDirRel = ".local/share/dotfiles-secrets"

// StorePath returns the encrypted store directory under the process home.
func StorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, StoreDirRel), nil
}

// sshKeyName returns the configured SSH key name, rejecting values that
// would escape ~/.ssh or the secrets store when interpolated into paths.
func sshKeyName(state *config.UserState) (string, error) {
	keyName := state.SSH.KeyName
	if keyName == "" {
		return "id_ed25519", nil
	}
	if strings.ContainsAny(keyName, "/\\") || keyName == "." || keyName == ".." {
		return "", fmt.Errorf("invalid ssh.key_name %q: must be a bare file name", keyName)
	}
	return keyName, nil
}

// ResolveAgeIdentity returns the age identity path (default
// ~/.ssh/id_ed25519) with a leading ~ expanded.
func ResolveAgeIdentity(state *config.UserState, home string) string {
	identity := state.Secrets.AgeIdentity
	if identity == "" {
		return filepath.Join(home, ".ssh", "id_ed25519")
	}
	if strings.HasPrefix(identity, "~/") {
		return filepath.Join(home, identity[2:])
	}
	if identity == "~" {
		return home
	}
	return identity
}

// Entry maps one encrypted archive name to its plaintext location.
type Entry struct {
	Label   string      // human-readable name for reports
	AgeName string      // file name inside the store / backup dir
	Plain   string      // plaintext path (encrypt source, restore dest)
	DirPerm os.FileMode // permission for the plaintext parent dir
}

// Entries is the single source of truth for which files `dot secrets`
// manages — init, restore, and status all derive from it.
func Entries(state *config.UserState, home string) ([]Entry, error) {
	keyName, err := sshKeyName(state)
	if err != nil {
		return nil, err
	}
	return []Entry{
		{
			Label:   "SSH key",
			AgeName: keyName + ".age",
			Plain:   filepath.Join(home, ".ssh", keyName),
			DirPerm: 0o700,
		},
		{
			Label:   "Shell secrets",
			AgeName: "90-secrets.sh.age",
			Plain:   filepath.Join(home, ".config", "shell", "90-secrets.sh"),
			DirPerm: 0o755,
		},
	}, nil
}
