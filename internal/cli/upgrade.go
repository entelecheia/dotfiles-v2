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
		Short:   "Update dotfiles binary to latest version",
		Long:    "Download and install the latest dotfiles release from GitHub.",
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

	fmt.Printf("Current version: %s\n", currentVersion)

	latest, err := fetchLatestRelease(ctx)
	if err != nil {
		return fmt.Errorf("checking latest version: %w", err)
	}

	latestVersion := strings.TrimPrefix(latest.TagName, "v")
	currentClean := strings.TrimPrefix(currentVersion, "v")

	fmt.Printf("Latest version:  %s\n", latestVersion)

	cmp := compareSemver(currentClean, latestVersion)
	switch {
	case cmp == 0:
		fmt.Println("Already up to date.")
		return nil
	case cmp > 0:
		fmt.Printf("Current version %s is newer than latest release %s.\n", currentClean, latestVersion)
		if checkOnly {
			return nil
		}
		fmt.Println("Nothing to upgrade.")
		return nil
	}

	if checkOnly {
		fmt.Printf("\nUpdate available: %s → %s\n", currentClean, latestVersion)
		fmt.Println("Run 'dotfiles update' to install.")
		return nil
	}

	osName := runtime.GOOS
	archName := runtime.GOARCH
	assetName := fmt.Sprintf("dotfiles_%s_%s_%s.tar.gz", latestVersion, osName, archName)
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", githubRepo, latest.TagName, assetName)

	fmt.Printf("\nDownloading %s...\n", assetName)

	tmpDir, err := os.MkdirTemp("", "dotfiles-upgrade-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	runner := exec.NewRunner(false, logger)
	if err := fileutil.DownloadAndExtractTarGz(ctx, runner, downloadURL, tmpDir, 0); err != nil {
		return fmt.Errorf("downloading release: %w", err)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}

	newBinary := filepath.Join(tmpDir, "dotfiles")
	if _, err := os.Stat(newBinary); err != nil {
		return fmt.Errorf("downloaded binary not found: %w", err)
	}

	// Sanity-check the downloaded binary before replacing the current one.
	// Runs '<new_binary> --version' with a 5s timeout and verifies it produces
	// some output containing "dotfiles". Catches wrong-arch/corrupted downloads.
	if err := verifyBinary(ctx, newBinary); err != nil {
		return fmt.Errorf("downloaded binary failed sanity check: %w", err)
	}

	data, err := os.ReadFile(newBinary)
	if err != nil {
		return fmt.Errorf("reading new binary: %w", err)
	}

	if err := os.WriteFile(execPath, data, 0755); err != nil {
		return fmt.Errorf("writing new binary: %w", err)
	}

	fmt.Printf("Upgraded: %s → %s\n", currentClean, latestVersion)
	fmt.Printf("Binary: %s\n", execPath)
	return nil
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
	if !strings.Contains(strings.ToLower(combined), "dotfiles") {
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
