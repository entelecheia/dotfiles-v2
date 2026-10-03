package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanVerifiedPrebuiltPinsNativeStableRelease(t *testing.T) {
	plan, err := planVerifiedPrebuilt("v2.70.35", "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if plan.AssetName != "dot_2.70.35_darwin_arm64.tar.gz" || plan.DownloadURL != "https://github.com/entelecheia/dotfiles-v2/releases/download/v2.70.35/dot_2.70.35_darwin_arm64.tar.gz" {
		t.Fatalf("unexpected release plan: %+v", plan)
	}
	for _, tag := range []string{"v2.70.35-rc.1", "v2.70.35+build", "latest", ""} {
		if _, err := planVerifiedPrebuilt(tag, "darwin", "arm64"); err == nil {
			t.Errorf("accepted non-stable release tag %q", tag)
		}
	}
	if _, err := planVerifiedPrebuilt("v2.70.35", "windows", "amd64"); err == nil {
		t.Fatal("accepted unsupported native platform")
	}
}

func TestParseUniqueChecksumRejectsAmbiguousOrMalformedEntries(t *testing.T) {
	const sum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got, err := parseUniqueChecksum([]byte(sum+"  dot.tar.gz\n"), "dot.tar.gz"); err != nil || got != sum {
		t.Fatalf("parseUniqueChecksum = %q, %v", got, err)
	}
	for _, body := range []string{
		sum + "  dot.tar.gz\n" + sum + "  dot.tar.gz\n",
		"xyz  dot.tar.gz\n",
		sum + "  other.tar.gz\n",
	} {
		if _, err := parseUniqueChecksum([]byte(body), "dot.tar.gz"); err == nil {
			t.Errorf("accepted invalid checksum listing %q", body)
		}
	}
}

func TestStandalonePromotionRequiresExactRuntimeVersion(t *testing.T) {
	run, acquire := upgradeRun, upgradeAcquirePrebuilt
	t.Cleanup(func() {
		upgradeRun, upgradeAcquirePrebuilt = run, acquire
	})
	upgradeRun = func(context.Context, bool, string, ...string) (string, error) {
		return "dot version 2.70.34 (abc)\n", nil
	}
	acquired := 0
	upgradeAcquirePrebuilt = func(context.Context) (func(), error) {
		acquired++
		return func() {}, nil
	}

	dir := t.TempDir()
	source := filepath.Join(dir, "new-dot")
	destination := filepath.Join(dir, "dot")
	if err := os.WriteFile(source, []byte("new executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installStandalonePrebuilt(context.Background(), &Printer{}, source, destination, "2.70.35"); err == nil {
		t.Fatal("accepted binary reporting the wrong release version")
	}
	if acquired != 0 {
		t.Fatalf("admission acquired %d times before exact version validation", acquired)
	}
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != "old executable" {
		t.Fatalf("destination changed after rejected binary: %q, %v", got, err)
	}
}

func TestExtractSinglePrebuiltRequiresOneRootExecutable(t *testing.T) {
	archive := tarGzFixture(t, []tar.Header{
		{Name: "dot", Typeflag: tar.TypeReg, Mode: 0o755, Size: 4},
	}, [][]byte{[]byte("ELF!")})
	path := filepath.Join(t.TempDir(), "dot")
	if err := extractSinglePrebuilt(bytes.NewReader(archive), path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "ELF!" {
		t.Fatalf("extracted bytes = %q, %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("executable mode = %v, %v", info, err)
	}

	for _, tc := range []struct {
		name    string
		headers []tar.Header
		bodies  [][]byte
	}{
		{"path traversal", []tar.Header{{Name: "../dot", Typeflag: tar.TypeReg, Mode: 0o755, Size: 4}}, [][]byte{[]byte("ELF!")}},
		{"extra member", []tar.Header{{Name: "dot", Typeflag: tar.TypeReg, Mode: 0o755, Size: 1}, {Name: "unexpected", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}}, [][]byte{[]byte("x"), []byte("y")}},
		{"symlink", []tar.Header{{Name: "dot", Typeflag: tar.TypeSymlink, Linkname: "elsewhere", Mode: 0o777}}, [][]byte{nil}},
		{"oversized executable", []tar.Header{{Name: "dot", Typeflag: tar.TypeReg, Mode: 0o755, Size: prebuiltMaxBinaryBytes + 1}}, [][]byte{nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "dot")
			archive := tarGzFixture(t, tc.headers, tc.bodies)
			if err := extractSinglePrebuilt(bytes.NewReader(archive), dest); err == nil {
				t.Fatal("accepted unsafe archive")
			}
			if _, err := os.Lstat(dest); !os.IsNotExist(err) {
				t.Fatalf("unsafe archive left output behind: %v", err)
			}
		})
	}
}

func tarGzFixture(t *testing.T, headers []tar.Header, bodies [][]byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tr := tar.NewWriter(gz)
	for i := range headers {
		h := headers[i]
		if err := tr.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if i < len(bodies) && len(bodies[i]) > 0 {
			if _, err := tr.Write(bodies[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func TestCanonicalHomebrewRecipeIsNativeOnlyAndExact(t *testing.T) {
	assets := []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"}
	var checksums strings.Builder
	for i, asset := range assets {
		fmt.Fprintf(&checksums, "%064x  dot_2.70.34_%s.tar.gz\n", i+1, asset)
	}
	source, err := canonicalGoReleaserFormula("2.70.34", []byte(checksums.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !canonicalFormulaMatches(source, "2.70.34", []byte(checksums.String())) {
		t.Fatal("accepted canonical generated formula")
	}
	for _, mutation := range []string{
		strings.Replace(source, `bin.install "dot"`, `system "make", "install"`, 1),
		strings.Replace(source, "class Dotfiles < Formula", "class Dotfiles < Formula\n  depends_on \"openssl\"", 1),
		strings.Replace(source, "  test do", "  post_install do\n    system \"sh\", \"-c\", \"touch /tmp/pwned\"\n  end\n\n  test do", 1),
		strings.Replace(source, "dot_2.70.34_darwin_arm64.tar.gz", "attacker.tar.gz", 1),
	} {
		if canonicalFormulaMatches(mutation, "2.70.34", []byte(checksums.String())) {
			t.Errorf("accepted changed Homebrew formula source:\n%s", mutation)
		}
	}
}

func TestPrebuiltFormulaInfoRejectsPinsAndDependencies(t *testing.T) {
	checksums := formulaChecksums("2.70.35")
	base := prebuiltBrewFormula{FullName: "entelecheia/tap/dotfiles"}
	base.Versions.Stable = "2.70.35"
	goos, goarch, err := supportedNativeReleaseTarget()
	if err != nil {
		t.Skip(err)
	}
	asset := fmt.Sprintf("dot_2.70.35_%s_%s.tar.gz", goos, goarch)
	base.URLs.Stable.URL = fmt.Sprintf("https://github.com/%s/releases/download/v2.70.35/%s", githubRepo, asset)
	base.URLs.Stable.Checksum, err = parseUniqueChecksum(checksums, asset)
	if err != nil {
		t.Fatal(err)
	}
	base.Installed = []struct {
		Version string `json:"version"`
	}{{Version: "2.70.34"}}
	if !prebuiltFormulaInfoEligible(base, "2.70.34", "2.70.35", checksums) {
		t.Fatal("rejected eligible native formula metadata")
	}
	for _, edit := range []func(*prebuiltBrewFormula){
		func(f *prebuiltBrewFormula) { f.Pinned = true },
		func(f *prebuiltBrewFormula) { f.Dependencies = []string{"openssl"} },
		func(f *prebuiltBrewFormula) { f.BuildDependencies = []string{"go"} },
		func(f *prebuiltBrewFormula) { f.PostInstallDefined = true },
		func(f *prebuiltBrewFormula) { f.Versions.Stable = "2.70.34" },
		func(f *prebuiltBrewFormula) { f.URLs.Stable.URL = "https://example.com/dot.tar.gz" },
		func(f *prebuiltBrewFormula) { f.URLs.Stable.Checksum = strings.Repeat("0", 64) },
		func(f *prebuiltBrewFormula) { f.Installed[0].Version = "2.70.33" },
	} {
		candidate := base
		edit(&candidate)
		if prebuiltFormulaInfoEligible(candidate, "2.70.34", "2.70.35", checksums) {
			t.Errorf("accepted unsafe formula metadata: %+v", candidate)
		}
	}
}

func formulaChecksums(version string) []byte {
	assets := []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"}
	var checksums strings.Builder
	for i, asset := range assets {
		fmt.Fprintf(&checksums, "%064x  dot_%s_%s.tar.gz\n", i+1, version, asset)
	}
	return []byte(checksums.String())
}
