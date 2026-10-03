package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var errHomebrewNeedsFullGate = errors.New("Homebrew metadata or recipe needs a full maintenance gate")

var prebuiltHomebrewEnvironment = []string{
	"HOMEBREW_NO_AUTO_UPDATE=1",
	"HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK=1",
	"HOMEBREW_NO_INSTALL_CLEANUP=1",
}

// upgradeHomebrewPrebuilt uses the warning-tolerant profile only when the
// current Homebrew tap contains the exact GoReleaser-generated native-only
// formula for this verified release. Any drift returns to the full-gate path.
func upgradeHomebrewPrebuilt(ctx context.Context, p *Printer, h homebrewDot, current, version string, checksums []byte, dryRun bool) error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("this dot binary belongs to Homebrew, which does not run as root; run dot update as the user who owns %s", h.prefix)
	}
	info, err := brewFormulaInfoNoAutoUpdate(ctx, h)
	if err != nil {
		return fmt.Errorf("%w: cannot read installed recipe: %v", errHomebrewNeedsFullGate, err)
	}
	if !prebuiltFormulaInfoEligible(info, current, version, checksums) || !prebuiltFormulaSourceEligible(ctx, h, info.FullName, version, checksums) {
		return errHomebrewNeedsFullGate
	}
	if dryRun {
		p.Line("[dry-run] verified Homebrew formula %s is eligible for dot %s", info.FullName, version)
		return nil
	}
	release, err := upgradeAcquirePrebuilt(ctx)
	if err != nil {
		return fmt.Errorf("the verified prebuilt Homebrew update needs the maintenance slot: %w; retry later", err)
	}
	defer release()
	// Re-read metadata and recipe after admission, immediately before brew can
	// mutate the managed installation.
	info, err = brewFormulaInfoNoAutoUpdate(ctx, h)
	if err != nil {
		return fmt.Errorf("%w: cannot re-read installed recipe: %v", errHomebrewNeedsFullGate, err)
	}
	if !prebuiltFormulaInfoEligible(info, current, version, checksums) || !prebuiltFormulaSourceEligible(ctx, h, info.FullName, version, checksums) {
		return errHomebrewNeedsFullGate
	}
	before, _ := filepath.EvalSymlinks(h.optDot())
	args := append(append([]string{}, prebuiltHomebrewEnvironment...), h.brew(), "upgrade", "--formula", info.FullName)
	p.Line("\nRunning verified Homebrew update for %s...", info.FullName)
	if _, err := upgradeRun(context.WithoutCancel(ctx), true, "/usr/bin/env", args...); err != nil {
		return fmt.Errorf("verified brew upgrade %s: %w", info.FullName, err)
	}
	installed, err := upgradedVersion(context.WithoutCancel(ctx), h, version)
	if err == nil && installed != version {
		err = fmt.Errorf("Homebrew installed %s, expected exact prebuilt version %s", installed, version)
	}
	if err == nil {
		p.Line("Upgraded to %s (Homebrew)", installed)
	}
	if after, _ := filepath.EvalSymlinks(h.optDot()); after == before {
		return err
	}
	return errors.Join(err, reloadDotLaunchAgents(context.WithoutCancel(ctx), p, h, false))
}

type prebuiltBrewFormula struct {
	FullName           string `json:"full_name"`
	Pinned             bool   `json:"pinned"`
	PostInstallDefined bool   `json:"post_install_defined"`
	Versions           struct {
		Stable string `json:"stable"`
	} `json:"versions"`
	URLs struct {
		Stable struct {
			URL      string `json:"url"`
			Checksum string `json:"checksum"`
		} `json:"stable"`
	} `json:"urls"`
	Installed []struct {
		Version string `json:"version"`
	} `json:"installed"`
	Dependencies            []string          `json:"dependencies"`
	BuildDependencies       []string          `json:"build_dependencies"`
	TestDependencies        []string          `json:"test_dependencies"`
	RecommendedDependencies []string          `json:"recommended_dependencies"`
	OptionalDependencies    []string          `json:"optional_dependencies"`
	UsesFromMacOS           []string          `json:"uses_from_macos"`
	Requirements            []json.RawMessage `json:"requirements"`
}

func brewFormulaInfoNoAutoUpdate(ctx context.Context, h homebrewDot) (prebuiltBrewFormula, error) {
	args := append(append([]string{}, prebuiltHomebrewEnvironment...), h.brew(), "info", "--json=v2", "--formula", h.formula)
	out, err := upgradeRun(ctx, false, "/usr/bin/env", args...)
	if err != nil {
		return prebuiltBrewFormula{}, fmt.Errorf("reading Homebrew metadata for %s: %w", h.formula, err)
	}
	var result struct {
		Formulae []prebuiltBrewFormula `json:"formulae"`
		Casks    []json.RawMessage     `json:"casks"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return prebuiltBrewFormula{}, fmt.Errorf("parsing Homebrew metadata: %w", err)
	}
	if len(result.Formulae) != 1 || result.Formulae[0].FullName == "" || len(result.Casks) != 0 {
		return prebuiltBrewFormula{}, fmt.Errorf("brew info returned %d formulae and %d casks, want one formula and no casks", len(result.Formulae), len(result.Casks))
	}
	return result.Formulae[0], nil
}

func prebuiltFormulaInfoEligible(info prebuiltBrewFormula, current, version string, checksums []byte) bool {
	goos, goarch, err := supportedNativeReleaseTarget()
	if err != nil {
		return false
	}
	asset := fmt.Sprintf("dot_%s_%s_%s.tar.gz", version, goos, goarch)
	wantHash, err := parseUniqueChecksum(checksums, asset)
	if err != nil {
		return false
	}
	wantURL := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", githubRepo, version, asset)
	return info.FullName != "" && !info.Pinned && info.Versions.Stable == version &&
		info.URLs.Stable.URL == wantURL && strings.EqualFold(info.URLs.Stable.Checksum, wantHash) &&
		len(info.Installed) == 1 && strings.TrimPrefix(info.Installed[0].Version, "v") == current &&
		len(info.Dependencies) == 0 && len(info.BuildDependencies) == 0 &&
		len(info.TestDependencies) == 0 && len(info.RecommendedDependencies) == 0 &&
		len(info.OptionalDependencies) == 0 && len(info.UsesFromMacOS) == 0 &&
		len(info.Requirements) == 0 && !info.PostInstallDefined
}

func prebuiltFormulaSourceEligible(ctx context.Context, h homebrewDot, fullName, version string, checksums []byte) bool {
	if !isTrustedTapFormula(fullName) {
		return false
	}
	args := append(append([]string{}, prebuiltHomebrewEnvironment...), h.brew(), "cat", fullName)
	out, err := upgradeRun(ctx, false, "/usr/bin/env", args...)
	if err != nil {
		return false
	}
	return canonicalFormulaMatches(out, version, checksums)
}

func canonicalFormulaMatches(source, version string, checksums []byte) bool {
	want, err := canonicalGoReleaserFormula(version, checksums)
	return err == nil && strings.TrimSpace(source) == strings.TrimSpace(want)
}

func isTrustedTapFormula(fullName string) bool {
	return fullName == "entelecheia/tap/dotfiles"
}

func canonicalGoReleaserFormula(version string, checksums []byte) (string, error) {
	type target struct{ os, arch string }
	targets := []target{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}}
	var sums []string
	for _, target := range targets {
		name := fmt.Sprintf("dot_%s_%s_%s.tar.gz", version, target.os, target.arch)
		sum, err := parseUniqueChecksum(checksums, name)
		if err != nil {
			return "", err
		}
		sums = append(sums, sum)
	}
	return fmt.Sprintf(`# typed: false
# frozen_string_literal: true

# This file was generated by GoReleaser. DO NOT EDIT.
class Dotfiles < Formula
  desc "Declarative dotfiles manager and AI tmux workspace orchestrator"
  homepage "https://github.com/entelecheia/dotfiles-v2"
  version "%s"
  license "MIT"

  on_macos do
    if Hardware::CPU.intel?
      url "https://github.com/entelecheia/dotfiles-v2/releases/download/v%s/dot_%s_darwin_amd64.tar.gz"
      sha256 "%s"

      define_method(:install) do
        bin.install "dot"
        bin.install_symlink "dot" => "dotfiles"
      end
    end
    if Hardware::CPU.arm?
      url "https://github.com/entelecheia/dotfiles-v2/releases/download/v%s/dot_%s_darwin_arm64.tar.gz"
      sha256 "%s"

      define_method(:install) do
        bin.install "dot"
        bin.install_symlink "dot" => "dotfiles"
      end
    end
  end

  on_linux do
    if Hardware::CPU.intel? && Hardware::CPU.is_64_bit?
      url "https://github.com/entelecheia/dotfiles-v2/releases/download/v%s/dot_%s_linux_amd64.tar.gz"
      sha256 "%s"
      define_method(:install) do
        bin.install "dot"
        bin.install_symlink "dot" => "dotfiles"
      end
    end
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/entelecheia/dotfiles-v2/releases/download/v%s/dot_%s_linux_arm64.tar.gz"
      sha256 "%s"
      define_method(:install) do
        bin.install "dot"
        bin.install_symlink "dot" => "dotfiles"
      end
    end
  end

  test do
    system "#{bin}/dot", "version"
  end
end
`, version, version, version, sums[0], version, version, sums[1], version, version, sums[2], version, version, sums[3]), nil
}

func supportedNativeReleaseTarget() (string, string, error) {
	if err := validatePrebuiltPlatform(runtime.GOOS); err != nil {
		return "", "", err
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return "", "", fmt.Errorf("verified prebuilt updates are unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return runtime.GOOS, runtime.GOARCH, nil
}

func discoverHomebrewDot(ctx context.Context) (homebrewDot, string, error) {
	brewPath, err := execLookPath("brew")
	if err != nil {
		return homebrewDot{}, "", fmt.Errorf("finding Homebrew for --homebrew: %w", err)
	}
	prefix, err := upgradeRun(ctx, false, brewPath, "--prefix")
	if err != nil {
		return homebrewDot{}, "", fmt.Errorf("finding Homebrew prefix: %w", err)
	}
	h := homebrewDot{prefix: strings.TrimSpace(prefix), formula: "dotfiles"}
	if h.prefix == "" {
		return homebrewDot{}, "", fmt.Errorf("Homebrew returned an empty prefix")
	}
	listed, err := upgradeRun(ctx, false, brewPath, "list", "--formula", "--versions", h.formula)
	if err != nil {
		return homebrewDot{}, "", fmt.Errorf("checking installed Homebrew dotfiles formula: %w", err)
	}
	fields := strings.Fields(listed)
	if len(fields) != 2 || fields[0] != h.formula {
		return homebrewDot{}, "", fmt.Errorf("Homebrew does not report one installed dotfiles formula: %q", strings.TrimSpace(listed))
	}
	version := strings.TrimPrefix(fields[1], "v")
	dot, err := filepath.EvalSymlinks(h.optDot())
	if err != nil {
		return homebrewDot{}, "", fmt.Errorf("resolving installed Homebrew dotfiles binary: %w", err)
	}
	managed, ok := homebrewDotFor(dot)
	if !ok || managed.formula != h.formula || filepath.Clean(managed.prefix) != filepath.Clean(h.prefix) {
		return homebrewDot{}, "", fmt.Errorf("Homebrew opt link does not resolve into the expected dotfiles formula")
	}
	installed, err := upgradedVersion(ctx, h, version)
	if err != nil {
		return homebrewDot{}, "", fmt.Errorf("Homebrew formula version %s does not match its opt binary: %w", version, err)
	}
	if installed != version {
		return homebrewDot{}, "", fmt.Errorf("Homebrew formula version %s does not match opt binary version %s", version, installed)
	}
	return h, version, nil
}

var execLookPath = exec.LookPath
