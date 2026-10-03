package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/resourceguard"
)

const (
	prebuiltMaxCompressedBytes = int64(32 << 20)
	prebuiltMaxBinaryBytes     = int64(64 << 20)
	prebuiltMaxExpandedBytes   = int64((64 << 20) + (1 << 20))
	prebuiltMaxMembers         = 1
	prebuiltMaxChecksumBytes   = int64(1 << 20)
)

var stableReleaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

var upgradeAcquirePrebuilt = resourceguard.AcquireVerifiedPrebuiltUpdate

type verifiedPrebuiltPlan struct {
	Version      string
	AssetName    string
	DownloadURL  string
	ChecksumsURL string
}

func planVerifiedPrebuilt(tag, goos, goarch string) (verifiedPrebuiltPlan, error) {
	if !stableReleaseTag.MatchString(tag) {
		return verifiedPrebuiltPlan{}, fmt.Errorf("release tag %q is not an exact stable version", tag)
	}
	if err := validatePrebuiltPlatform(goos); err != nil {
		return verifiedPrebuiltPlan{}, err
	}
	if goarch != "amd64" && goarch != "arm64" {
		return verifiedPrebuiltPlan{}, fmt.Errorf("verified prebuilt updates are unsupported on %s/%s", goos, goarch)
	}
	version := strings.TrimPrefix(tag, "v")
	asset := fmt.Sprintf("dot_%s_%s_%s.tar.gz", version, goos, goarch)
	base := "https://github.com/" + githubRepo + "/releases/download/" + tag + "/"
	return verifiedPrebuiltPlan{Version: version, AssetName: asset, DownloadURL: base + asset, ChecksumsURL: base + "checksums.txt"}, nil
}

func validatePrebuiltPlatform(goos string) error {
	if goos != "darwin" && goos != "linux" {
		return fmt.Errorf("verified prebuilt updates are unsupported on %s", goos)
	}
	return nil
}

func isTrustedReleaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Host == "github.com" && strings.HasPrefix(u.Path, "/"+githubRepo+"/releases/download/")
}

func prebuiltHTTPClient(timeout time.Duration) *http.Client {
	allowed := map[string]bool{
		"github.com": true, "release-assets.githubusercontent.com": true,
		"objects.githubusercontent.com": true,
	}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || !allowed[req.URL.Hostname()] {
				return fmt.Errorf("untrusted release redirect to %s", req.URL.Redacted())
			}
			if len(via) >= 5 {
				return fmt.Errorf("too many release redirects")
			}
			return nil
		},
	}
}

func downloadVerifiedPrebuilt(ctx context.Context, plan verifiedPrebuiltPlan, dir string) (string, []byte, error) {
	checksums, err := fetchPrebuiltChecksums(ctx, plan.ChecksumsURL)
	if err != nil {
		return "", nil, err
	}
	return downloadVerifiedPrebuiltWithChecksums(ctx, plan, dir, checksums)
}

func downloadVerifiedPrebuiltWithChecksums(ctx context.Context, plan verifiedPrebuiltPlan, dir string, checksums []byte) (string, []byte, error) {
	goos, goarch, err := supportedNativeReleaseTarget()
	if err != nil {
		return "", nil, err
	}
	want, err := planVerifiedPrebuilt("v"+plan.Version, goos, goarch)
	if err != nil || plan != want {
		return "", nil, fmt.Errorf("release plan does not match the exact native GitHub release")
	}
	archivePath := filepath.Join(dir, plan.AssetName)
	sum, size, err := downloadPrebuiltAsset(ctx, plan.DownloadURL, archivePath)
	if err != nil {
		return "", nil, err
	}
	if size > prebuiltMaxCompressedBytes {
		return "", nil, fmt.Errorf("release archive is %d bytes, maximum is %d", size, prebuiltMaxCompressedBytes)
	}
	want, err := parseUniqueChecksum(checksums, plan.AssetName)
	if err != nil {
		return "", nil, err
	}
	if !strings.EqualFold(sum, want) {
		return "", nil, fmt.Errorf("checksum mismatch for %s", plan.AssetName)
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		return "", nil, err
	}
	defer archive.Close()
	binaryPath := filepath.Join(dir, "dot")
	if err := extractSinglePrebuilt(archive, binaryPath); err != nil {
		return "", nil, err
	}
	return binaryPath, checksums, nil
}

func downloadPrebuiltAsset(ctx context.Context, rawURL, dest string) (string, int64, error) {
	if !isTrustedReleaseURL(rawURL) {
		return "", 0, fmt.Errorf("untrusted release URL")
	}
	client := prebuiltHTTPClient(2 * time.Minute)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", 0, err
		}
		resp, err := client.Do(req)
		if err == nil {
			if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("release archive returned HTTP %d", resp.StatusCode)
				_ = resp.Body.Close()
			} else if resp.ContentLength > prebuiltMaxCompressedBytes {
				_ = resp.Body.Close()
				return "", 0, fmt.Errorf("release archive exceeds %d bytes", prebuiltMaxCompressedBytes)
			} else {
				sum, size, saveErr := saveBoundedDownload(resp.Body, dest, prebuiltMaxCompressedBytes)
				closeErr := resp.Body.Close()
				if saveErr == nil && closeErr == nil {
					return sum, size, nil
				}
				lastErr = errors.Join(saveErr, closeErr)
				_ = os.Remove(dest)
			}
		} else {
			lastErr = err
		}
		if attempt < 2 {
			select {
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			case <-ctx.Done():
				return "", 0, ctx.Err()
			}
		}
	}
	return "", 0, fmt.Errorf("downloading verified release: %w", lastErr)
}

func saveBoundedDownload(r io.Reader, dest string, limit int64) (string, int64, error) {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, limit+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		return "", n, errors.Join(copyErr, closeErr)
	}
	if n > limit {
		return "", n, fmt.Errorf("download exceeded %d bytes", limit)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func fetchPrebuiltChecksums(ctx context.Context, rawURL string) ([]byte, error) {
	if !isTrustedReleaseURL(rawURL) {
		return nil, fmt.Errorf("untrusted checksums URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := prebuiltHTTPClient(15 * time.Second).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching release checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("checksums returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > prebuiltMaxChecksumBytes {
		return nil, fmt.Errorf("checksums file exceeds %d bytes", prebuiltMaxChecksumBytes)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, prebuiltMaxChecksumBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > prebuiltMaxChecksumBytes {
		return nil, fmt.Errorf("checksums file exceeds %d bytes", prebuiltMaxChecksumBytes)
	}
	return body, nil
}

func parseUniqueChecksum(data []byte, asset string) (string, error) {
	var found string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != asset {
			continue
		}
		sum := fields[0]
		if len(sum) != sha256.Size*2 {
			return "", fmt.Errorf("invalid checksum entry for %s", asset)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return "", fmt.Errorf("invalid checksum entry for %s", asset)
		}
		if found != "" {
			return "", fmt.Errorf("duplicate checksum entries for %s", asset)
		}
		found = sum
	}
	if found == "" {
		return "", fmt.Errorf("asset %s not listed in checksums.txt", asset)
	}
	return found, nil
}

func extractSinglePrebuilt(r io.Reader, destination string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("opening release archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	count := 0
	var expanded int64
	var binarySize int64
	var file *os.File
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if file != nil {
				_ = file.Close()
				_ = os.Remove(destination)
			}
			return fmt.Errorf("reading release archive: %w", err)
		}
		count++
		if count > prebuiltMaxMembers || h.Name != "dot" || h.Typeflag != tar.TypeReg || h.Size <= 0 || h.Size > prebuiltMaxBinaryBytes || h.Mode&07000 != 0 {
			if file != nil {
				_ = file.Close()
				_ = os.Remove(destination)
			}
			return fmt.Errorf("release archive must contain only one regular root dot executable no larger than %d bytes", prebuiltMaxBinaryBytes)
		}
		expanded += h.Size
		if expanded > prebuiltMaxExpandedBytes {
			return fmt.Errorf("release archive expands beyond %d bytes", prebuiltMaxExpandedBytes)
		}
		binarySize = h.Size
		file, err = os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return fmt.Errorf("creating extracted executable: %w", err)
		}
		written, copyErr := io.Copy(file, io.LimitReader(tr, prebuiltMaxBinaryBytes+1))
		if copyErr != nil {
			_ = file.Close()
			_ = os.Remove(destination)
			return fmt.Errorf("reading release executable: %w", copyErr)
		}
		if written != binarySize {
			_ = file.Close()
			_ = os.Remove(destination)
			return fmt.Errorf("release executable has invalid size: wrote %d of %d bytes", written, binarySize)
		}
	}
	var tail [32 * 1024]byte
	var trailingBytes int64
	for {
		n, readErr := gz.Read(tail[:])
		trailingBytes += int64(n)
		if expanded+trailingBytes > prebuiltMaxExpandedBytes {
			if file != nil {
				_ = file.Close()
				_ = os.Remove(destination)
			}
			return fmt.Errorf("release archive expands beyond %d bytes", prebuiltMaxExpandedBytes)
		}
		for _, b := range tail[:n] {
			if b != 0 {
				if file != nil {
					_ = file.Close()
					_ = os.Remove(destination)
				}
				return fmt.Errorf("release archive contains trailing non-tar data")
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if file != nil {
				_ = file.Close()
				_ = os.Remove(destination)
			}
			return fmt.Errorf("reading release archive trailer: %w", readErr)
		}
	}
	if count != prebuiltMaxMembers || file == nil {
		return fmt.Errorf("release archive contains %d members, want exactly %d", count, prebuiltMaxMembers)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(destination)
		return err
	}
	if err := os.Chmod(destination, 0o755); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return nil
}

func verifyExactPrebuiltBinary(ctx context.Context, path, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := upgradeRun(ctx, false, path, "--version")
	if err != nil {
		return fmt.Errorf("running verified release --version: %w", err)
	}
	fields := strings.Fields(out)
	if len(fields) < 3 || fields[0] != "dot" || fields[1] != "version" || strings.TrimPrefix(fields[2], "v") != version {
		return fmt.Errorf("verified executable reports %q, expected exact dot version %s", strings.TrimSpace(out), version)
	}
	return nil
}

func installStandalonePrebuilt(ctx context.Context, p *Printer, source, destination, version string) error {
	if err := verifyExactPrebuiltBinary(ctx, source, version); err != nil {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if int64(len(data)) == 0 || int64(len(data)) > prebuiltMaxBinaryBytes {
		return fmt.Errorf("verified executable size %d is outside the prebuilt limit", len(data))
	}
	release, err := upgradeAcquirePrebuilt(ctx)
	if err != nil {
		return fmt.Errorf("the verified prebuilt update needs the maintenance slot: %w; retry later", err)
	}
	defer release()
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".dot.*.new")
	if err != nil {
		return fmt.Errorf("creating staging file in %s: %w", filepath.Dir(destination), err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing staging executable: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		cleanup()
		return fmt.Errorf("replacing binary %s: %w", destination, err)
	}
	p.Line("Upgraded to %s", version)
	p.Line("Binary: %s", destination)
	return nil
}
