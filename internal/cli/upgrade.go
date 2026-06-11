package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

const githubRepo = "entelecheia/dotfiles-v2"

func newUpgradeCmd(currentVersion string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "update",
		Aliases: []string{"upgrade"},
		Short:   "Update dot binary to latest version",
		Long:    "Download and install the latest dot release from GitHub.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgrade(cmd, currentVersion)
		},
	}
	cmd.Flags().Bool("check", false, "Only check for updates without installing")
	return cmd
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
}

func runUpgrade(cmd *cobra.Command, currentVersion string) error {
	ctx := context.Background()
	checkOnly, _ := cmd.Flags().GetBool("check")
	p := printerFrom(cmd)

	p.Line("Current version: %s", currentVersion)

	latest, err := fetchLatestRelease(ctx)
	if err != nil {
		return fmt.Errorf("checking latest version: %w", err)
	}

	latestVersion := strings.TrimPrefix(latest.TagName, "v")
	currentClean := strings.TrimPrefix(currentVersion, "v")

	p.Line("Latest version:  %s", latestVersion)

	cmp := compareSemver(currentClean, latestVersion)
	switch {
	case cmp == 0:
		p.Line("Already up to date.")
		return nil
	case cmp > 0:
		p.Line("Current version %s is newer than latest release %s.", currentClean, latestVersion)
		if checkOnly {
			return nil
		}
		p.Line("Nothing to upgrade.")
		return nil
	}

	if checkOnly {
		p.Line("\nUpdate available: %s → %s", currentClean, latestVersion)
		p.Line("Run 'dot update' to install.")
		return nil
	}

	osName := runtime.GOOS
	archName := runtime.GOARCH
	assetName := fmt.Sprintf("dot_%s_%s_%s.tar.gz", latestVersion, osName, archName)
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", githubRepo, latest.TagName, assetName)
	checksumsURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/checksums.txt", githubRepo, latest.TagName)

	p.Line("\nDownloading %s...", assetName)

	tmpDir, err := os.MkdirTemp("", "dot-upgrade-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	runner := exec.NewRunner(false, logger)
	p.Line("Verifying checksum...")
	if err := downloadVerifiedArchive(ctx, runner, downloadURL, checksumsURL, assetName, tmpDir); err != nil {
		return err
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}

	newBinary := filepath.Join(tmpDir, "dot")
	if _, err := os.Stat(newBinary); err != nil {
		return fmt.Errorf("downloaded binary not found: %w", err)
	}

	// Sanity-check the downloaded binary before replacing the current one.
	// Runs '<new_binary> --version' with a 5s timeout and verifies it produces
	// some output starting with "dot ". Catches wrong-arch/corrupted downloads.
	if err := verifyBinary(ctx, newBinary); err != nil {
		return fmt.Errorf("downloaded binary failed sanity check: %w", err)
	}

	// Atomic replace: write a sibling of execPath, then rename over it.
	// A direct write(2) to the running binary fails with ETXTBSY on Linux;
	// rename(2) only swaps the directory entry, leaving the running process
	// mapped to the old inode so it can finish cleanly.
	data, err := os.ReadFile(newBinary)
	if err != nil {
		return fmt.Errorf("reading new binary: %w", err)
	}

	dir := filepath.Dir(execPath)
	tmp, err := os.CreateTemp(dir, ".dot.*.new")
	if err != nil {
		return fmt.Errorf("creating staging file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing staging file: %w", err)
	}
	if err := tmp.Chmod(0755); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod staging file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing staging file: %w", err)
	}
	if err := os.Rename(tmpPath, execPath); err != nil {
		cleanup()
		return fmt.Errorf("replacing binary %s: %w", execPath, err)
	}

	p.Line("Upgraded: %s → %s", currentClean, latestVersion)
	p.Line("Binary: %s", execPath)
	return nil
}

// downloadVerifiedArchive downloads the release archive and the release's
// checksums.txt, verifies the archive's sha256, then extracts into destDir.
// Any failure leaves destDir without an extracted binary — a missing or
// unfetchable checksums.txt is a hard failure, never a silent skip.
func downloadVerifiedArchive(ctx context.Context, runner *exec.Runner, downloadURL, checksumsURL, assetName, destDir string) error {
	if runner.DryRun {
		runner.Logger.Info("dry-run: download+verify+extract", "url", downloadURL, "dest", destDir)
		return nil
	}

	archivePath := filepath.Join(destDir, assetName)
	gotSum, err := fileutil.DownloadFile(ctx, runner, downloadURL, archivePath)
	if err != nil {
		return fmt.Errorf("downloading release: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumsURL, nil)
	if err != nil {
		return fmt.Errorf("creating checksums request: %w", err)
	}
	resp, err := httpDoWithRetry(req, 3)
	if err != nil {
		return fmt.Errorf("fetching checksums.txt: %w — refusing to install unverified binary", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching checksums.txt (HTTP %d): refusing to install unverified binary", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading checksums.txt: %w", err)
	}

	wantSum, err := parseChecksums(body, assetName)
	if err != nil {
		return err
	}
	if !strings.EqualFold(gotSum, wantSum) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s — aborting upgrade", assetName, wantSum, gotSum)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening verified archive: %w", err)
	}
	defer f.Close()
	return fileutil.ExtractTarGz(f, destDir, 0)
}

// parseChecksums finds assetName in a GoReleaser checksums.txt body
// ("<64-hex-sha256>  <asset>" per line, optional '*' binary-mode prefix)
// and returns its hex sha256.
func parseChecksums(data []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sum := fields[0]
		if len(sum) != 64 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == assetName {
			return sum, nil
		}
	}
	return "", fmt.Errorf("asset %s not listed in checksums.txt", assetName)
}

// verifyBinary runs the binary with --version and checks for expected output.
func verifyBinary(ctx context.Context, path string) error {
	verifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	runner := exec.NewRunner(false, slog.Default())
	res, err := runner.RunQuery(verifyCtx, path, "--version")
	if err != nil {
		return fmt.Errorf("running %s --version: %w", path, err)
	}
	combined := res.Stdout + res.Stderr
	if !strings.Contains(strings.ToLower(combined), "dot version") {
		return fmt.Errorf("unexpected version output: %s", strings.TrimSpace(combined))
	}
	return nil
}

func fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpDoWithRetry(req, 3)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &release, nil
}

// httpDoWithRetry performs an HTTP request with exponential backoff on transient failures.
// Retries on network errors and 5xx responses. 4xx responses are returned immediately (client error).
func httpDoWithRetry(req *http.Request, attempts int) (*http.Response, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			if resp.StatusCode < 500 {
				return resp, nil
			}
			// 5xx: transient, retry
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		if i < attempts-1 {
			time.Sleep(time.Duration(1<<i) * time.Second) // 1s, 2s, 4s
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", attempts, lastErr)
}

// parseSemver parses "major.minor.patch" (with optional leading "v" already stripped).
// Returns (0, 0, 0, false) for non-semver inputs like "dev" or empty.
func parseSemver(s string) (major, minor, patch int, ok bool) {
	// Strip any pre-release/build metadata
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var err error
	if major, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, 0, false
	}
	if minor, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, 0, false
	}
	if patch, err = strconv.Atoi(parts[2]); err != nil {
		return 0, 0, 0, false
	}
	return major, minor, patch, true
}

// compareSemver returns -1 if a < b, 0 if equal, 1 if a > b.
// Non-semver versions (e.g. "dev") are treated as older than any real version,
// so dev < 0.0.0 → -1, triggering upgrade.
func compareSemver(a, b string) int {
	am, an, ap, aOK := parseSemver(a)
	bm, bn, bp, bOK := parseSemver(b)

	if !aOK && !bOK {
		if a == b {
			return 0
		}
		return -1 // treat unparseable as older
	}
	if !aOK {
		return -1 // dev is older than any release
	}
	if !bOK {
		return 1
	}

	if am != bm {
		if am < bm {
			return -1
		}
		return 1
	}
	if an != bn {
		if an < bn {
			return -1
		}
		return 1
	}
	if ap != bp {
		if ap < bp {
			return -1
		}
		return 1
	}
	return 0
}
