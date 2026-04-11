package fileutil

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

const refreshFile = ".dotfiles-refresh"

// httpGetWithRetry performs an HTTP GET with exponential backoff on transient failures.
// Retries on network errors and 5xx responses. 4xx responses returned immediately.
func httpGetWithRetry(ctx context.Context, url string, attempts int) (*http.Response, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			if resp.StatusCode < 500 {
				return resp, nil
			}
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		if i < attempts-1 {
			time.Sleep(time.Duration(1<<i) * time.Second)
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", attempts, lastErr)
}

// DownloadAndExtractTarGz downloads a .tar.gz archive and extracts it.
func DownloadAndExtractTarGz(ctx context.Context, runner *exec.Runner, url, destDir string, stripComponents int) error {
	if runner.DryRun {
		runner.Logger.Info("dry-run: download+extract", "url", url, "dest", destDir)
		return nil
	}

	resp, err := httpGetWithRetry(ctx, url, 3)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}

		// Strip leading path components
		name := hdr.Name
		parts := strings.SplitN(name, "/", stripComponents+1)
		if len(parts) <= stripComponents {
			continue
		}
		name = parts[stripComponents]
		if name == "" {
			continue
		}

		target := filepath.Join(destDir, name)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			os.Remove(target) // remove existing symlink if any
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}

	return nil
}

// DownloadAndExtractZip downloads a .zip archive and extracts it.
func DownloadAndExtractZip(ctx context.Context, runner *exec.Runner, url, destDir string) error {
	if runner.DryRun {
		runner.Logger.Info("dry-run: download+extract zip", "url", url, "dest", destDir)
		return nil
	}

	// Download to temp file
	resp, err := httpGetWithRetry(ctx, url, 3)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "dotfiles-*.zip")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return fmt.Errorf("saving download: %w", err)
	}
	tmpFile.Close()

	// Extract
	r, err := zip.OpenReader(tmpFile.Name())
	if err != nil {
		return fmt.Errorf("opening zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name)

		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}

		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

// NeedsRefresh checks if a resource needs refreshing based on a timestamp file.
func NeedsRefresh(dir string, period time.Duration) bool {
	path := filepath.Join(dir, refreshFile)
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) > period
}

// MarkRefreshed updates the refresh timestamp.
func MarkRefreshed(runner *exec.Runner, dir string) error {
	path := filepath.Join(dir, refreshFile)
	return runner.WriteFile(path, []byte(time.Now().Format(time.RFC3339)), 0644)
}
