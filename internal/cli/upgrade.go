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
	"strings"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

const githubRepo = "entelecheia/dotfiles-v2"

func newUpgradeCmd(currentVersion string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "upgrade",
		Aliases: []string{"update"},
		Short:   "Upgrade dotfiles binary to latest version",
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

	// Fetch latest release info
	latest, err := fetchLatestRelease(ctx)
	if err != nil {
		return fmt.Errorf("checking latest version: %w", err)
	}

	latestVersion := strings.TrimPrefix(latest.TagName, "v")
	currentClean := strings.TrimPrefix(currentVersion, "v")

	fmt.Printf("Latest version:  %s\n", latestVersion)

	if currentClean == latestVersion {
		fmt.Println("Already up to date.")
		return nil
	}

	if checkOnly {
		fmt.Printf("\nUpdate available: %s → %s\n", currentClean, latestVersion)
		fmt.Println("Run 'dotfiles upgrade' to install.")
		return nil
	}

	// Determine download URL
	osName := runtime.GOOS
	archName := runtime.GOARCH
	assetName := fmt.Sprintf("dotfiles_%s_%s_%s.tar.gz", latestVersion, osName, archName)
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", githubRepo, latest.TagName, assetName)

	fmt.Printf("\nDownloading %s...\n", assetName)

	// Download to temp directory
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

	// Find current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}

	// Replace current binary
	newBinary := filepath.Join(tmpDir, "dotfiles")
	if _, err := os.Stat(newBinary); err != nil {
		return fmt.Errorf("downloaded binary not found: %w", err)
	}

	// Read new binary into memory, then write to current path
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

func fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	// Use GITHUB_TOKEN if available
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
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
