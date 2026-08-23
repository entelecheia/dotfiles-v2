package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// PeerStatusSchemaVersion is the schema version of the `dot peer status --json`
// document. The encoder lives in cli (D-12); the version is shared because the
// coordinator check below reads the same document off the remote machine.
const PeerStatusSchemaVersion = 1

// peerRemoteInventory uses an empty dry-run destination as a portable remote
// listing. `rsync --list-only` ignores --out-format on Apple's rsync, while a
// dry-run against an empty destination emits every remote file through the
// same rsync implementation used by the real transfer. No remote state is
// changed and paths omitted from the listing are unambiguously absent.
func peerRemoteInventory(ctx context.Context, runner *exec.Runner, cfg *Config, baseline map[string]Fingerprint) (PeerSnapshot, error) {
	if cfg == nil || !cfg.Target.IsSSH() {
		return nil, fmt.Errorf("peer inventory: target is not SSH")
	}
	root, err := os.MkdirTemp("", "dot-peer-inventory-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)

	args := []string{"-r", "--dry-run", "--no-links", "--out-format=@@%l\t%M\t%n"}
	args = append(args, PeerFilterArgs(cfg)...)
	remoteRsync := cfg.RemoteRsyncPath
	if remoteRsync == "" {
		remoteRsync = "rsync"
	}
	// Force both rsync processes to render %M in UTC. A fixed offset obtained
	// from `date +%z` is wrong for historical timestamps across DST changes.
	args = append(args, "--rsync-path=env TZ=UTC "+remoteRsync)
	args = append(args, "-e", "ssh -o BatchMode=yes -o ConnectTimeout=5", cfg.Target.RsyncDest(), root+"/")
	res, runErr := runner.Run(ctx, "env", append([]string{"TZ=UTC", "rsync"}, args...)...)
	if runErr != nil {
		return nil, fmt.Errorf("peer inventory: %w", runErr)
	}
	requireNFD := NFDMigrationMarked(cfg.LocalPaths.WorkspaceRoot)
	return parsePeerRemoteInventory(res.Stdout, time.UTC, baseline, requireNFD)
}

const remotePeerDotResolver = `set -eu
dot_bin=
for candidate in "$HOME/.local/bin/dot" /opt/homebrew/bin/dot /usr/local/bin/dot /home/linuxbrew/.linuxbrew/bin/dot; do
  if [ -x "$candidate" ]; then
    dot_bin=$candidate
    break
  fi
done
if [ -z "$dot_bin" ]; then
  dot_bin=$(command -v dot 2>/dev/null || true)
fi
if [ -z "$dot_bin" ] || [ ! -x "$dot_bin" ]; then
  echo "peer dot binary is missing from supported install locations" >&2
  exit 127
fi`

const remotePeerStatusCommand = remotePeerDotResolver + `
exec "$dot_bin" peer status --json`

const remotePeerNormalizeCommand = remotePeerDotResolver + `
exec "$dot_bin" sync names normalize --profile=peer --yes`

// remotePeerStatus is the narrow view of a remote `dot peer status --json`
// document the coordinator check reads. The full document is assembled by the
// cli encoder (D-12); decoding only the coordinator fields keeps the engine
// off that type. Unknown fields are ignored, as they were before the move.
type remotePeerStatus struct {
	SchemaVersion int    `json:"schemaVersion"`
	Kind          string `json:"kind"`
	Profile       struct {
		Configured    bool   `json:"configured"`
		Owner         string `json:"owner"`
		WorkspacePath string `json:"workspacePath"`
		Target        struct {
			Path string `json:"path"`
		} `json:"target"`
	} `json:"profile"`
}

// checkRemotePeerOwner makes the single-coordinator invariant bilateral. A
// local owner guard alone is insufficient: two independently initialized Macs
// can each name themselves and both scheduled jobs would pass their own guard.
func checkRemotePeerOwner(ctx context.Context, runner *exec.Runner, cfg *Config) error {
	if cfg == nil || !cfg.Target.IsSSH() {
		return fmt.Errorf("peer coordinator check: target is not SSH")
	}
	if strings.TrimSpace(cfg.Owner) == "" {
		return fmt.Errorf("peer coordinator check: local peer owner is empty; set one with `dot sync owner --profile=peer --set <coordinator>`")
	}
	res, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", cfg.Target.Host, remotePeerStatusCommand)
	if err != nil {
		return fmt.Errorf("peer coordinator check: reading remote peer status: %w", err)
	}
	return validateRemotePeerStatus(cfg, res.Stdout)
}

func validateRemotePeerStatus(cfg *Config, raw string) error {
	var status remotePeerStatus
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		return fmt.Errorf("peer coordinator check: invalid remote status JSON: %w", err)
	}
	if status.SchemaVersion != PeerStatusSchemaVersion || status.Kind != "peer" || !status.Profile.Configured {
		return fmt.Errorf("peer coordinator check: remote peer profile is not configured with the supported schema")
	}
	wantOwner := NormalizeHostname(cfg.Owner)
	gotOwner := NormalizeHostname(status.Profile.Owner)
	if wantOwner == "" || gotOwner != wantOwner {
		return fmt.Errorf(
			"peer coordinator check: both profiles must name the same owner (local %q, remote %q); set the remote profile to %q and keep its scheduler off",
			cfg.Owner, status.Profile.Owner, cfg.Owner)
	}
	remoteWorkspace := filepath.Clean(status.Profile.WorkspacePath)
	wantRemoteWorkspace := filepath.Clean(cfg.Target.Path)
	remoteTarget := filepath.Clean(status.Profile.Target.Path)
	wantRemoteTarget := filepath.Clean(strings.TrimRight(cfg.LocalPath, "/"))
	if remoteWorkspace != wantRemoteWorkspace || remoteTarget != wantRemoteTarget {
		return fmt.Errorf(
			"peer coordinator check: remote profile does not point back to this workspace (remote workspace %q target %q; expected %q -> %q)",
			status.Profile.WorkspacePath, status.Profile.Target.Path, wantRemoteWorkspace, wantRemoteTarget)
	}
	return nil
}

func normalizeRemotePeerNames(ctx context.Context, runner *exec.Runner, cfg *Config) error {
	if _, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", cfg.Target.Host, remotePeerNormalizeCommand); err != nil {
		return fmt.Errorf("normalizing peer workspace names: %w", err)
	}
	return nil
}

func parsePeerRemoteInventory(stdout string, remoteLoc *time.Location, baseline map[string]Fingerprint, requireNFD bool) (PeerSnapshot, error) {
	out := PeerSnapshot{}
	for _, raw := range strings.Split(stdout, "\n") {
		if raw == "" {
			continue
		}
		if !strings.HasPrefix(raw, "@@") {
			const skippedPrefix = "skipping non-regular file "
			if strings.HasPrefix(raw, skippedPrefix) {
				skipped, err := strconv.Unquote(strings.TrimPrefix(raw, skippedPrefix))
				if err != nil {
					return nil, fmt.Errorf("peer inventory: malformed non-regular warning %q", raw)
				}
				rel, err := cleanPeerInventoryRel(skipped, false)
				if err != nil {
					return nil, err
				}
				if _, tracked := baseline[rel]; tracked {
					return nil, fmt.Errorf("peer inventory: baseline path %q is non-regular on the peer", rel)
				}
				// Unknown symlinks and sockets were never payloads. --no-links
				// deliberately omits them, so they cannot be edits or deletions.
				continue
			}
			// Stdout is reserved for the machine-readable --out-format. Treat a
			// continuation from a newline-bearing filename, or any other surprise,
			// as an incomplete inventory and fail closed.
			return nil, fmt.Errorf("peer inventory: unexpected rsync output %q", raw)
		}
		parts := strings.SplitN(strings.TrimPrefix(raw, "@@"), "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("peer inventory: malformed rsync record %q", raw)
		}
		rawRel := parts[2]
		isDir := strings.HasSuffix(rawRel, "/")
		rel, err := cleanPeerInventoryRel(rawRel, isDir)
		if err != nil {
			return nil, err
		}
		if requireNFD && rel != "" && !NFDPathNormalized(rel) {
			return nil, fmt.Errorf("peer inventory: path %q is not NFD-normalized; normalize the peer before retrying", rel)
		}
		// The dry-run listing includes directory traversal records. Inventory is
		// file-only, so skip them before trimming the rsync directory marker.
		// Keeping this check before TrimSuffix is important: treating a directory
		// as a zero-byte file would manufacture a peer change for every subtree.
		if isDir || rel == "" {
			continue
		}
		size, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("peer inventory: malformed size %q: %w", parts[0], err)
		}
		if size < 0 {
			return nil, fmt.Errorf("peer inventory: malformed negative size %q", parts[0])
		}
		mtime, err := parsePeerRsyncTime(parts[1], remoteLoc)
		if err != nil {
			return nil, fmt.Errorf("peer inventory: malformed mtime for %q: %w", rel, err)
		}
		if _, duplicate := out[rel]; duplicate {
			return nil, fmt.Errorf("peer inventory: duplicate path %q", rel)
		}
		out[filepath.ToSlash(rel)] = PeerFile{
			Present: true,
			FP:      Fingerprint{Size: size, Mtime: mtime},
		}
	}
	return out, nil
}

func cleanPeerInventoryRel(raw string, isDir bool) (string, error) {
	if strings.ContainsAny(raw, "\x00\r\n\t") {
		return "", fmt.Errorf("peer inventory: unsafe path %q", raw)
	}
	rel := strings.TrimPrefix(raw, "./")
	if isDir {
		rel = strings.TrimSuffix(rel, "/")
	}
	if rel == "" || rel == "." {
		return "", nil
	}
	if filepath.IsAbs(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("peer inventory: unsafe path %q", raw)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != filepath.ToSlash(rel) {
		return "", fmt.Errorf("peer inventory: unsafe path %q", raw)
	}
	return clean, nil
}

func parsePeerRsyncTime(raw string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	for _, layout := range []string{"2006/01/02-15:04:05", "2006/01/02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(raw), loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported rsync time %q", raw)
}

func intersectPeerPaths(a, b []string) []string {
	want := make(map[string]bool, len(b))
	for _, rel := range b {
		want[rel] = true
	}
	out := make([]string, 0, len(a))
	for _, rel := range a {
		if want[rel] {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// peerClockSkew reports peer-time minus local-time.
func peerClockSkew(ctx context.Context, runner *exec.Runner, host string) (time.Duration, error) {
	res, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", host, "date -u +%s")
	if err != nil {
		return 0, err
	}
	remote, err := strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unexpected peer clock output %q", strings.TrimSpace(res.Stdout))
	}
	return time.Duration(remote-time.Now().UTC().Unix()) * time.Second, nil
}
