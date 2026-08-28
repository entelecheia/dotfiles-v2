package module

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalexec "github.com/entelecheia/dotfiles-v2/internal/exec"
)

func TestFontsManifest_AllSelectableFamiliesPinned(t *testing.T) {
	want := map[string]string{
		"FiraCode":      "239395baf60c89b2eaf4862b6b09db0ef95605cd3e8eef51c00345822a81a665",
		"JetBrainsMono": "fab782a66f7d3019da64f6572db9fc5d3a4bcb19f9fa13e2d8a62e3693d6396e",
		"Hack":          "fa24da7de7cefe7766614d27762570b20453c852fc1d5b657111666df9a5e449",
	}
	if len(nerdFontPins) != len(want) {
		t.Fatalf("font pin count = %d, want %d", len(nerdFontPins), len(want))
	}
	for _, pin := range nerdFontPins {
		if pin.Tag != "v3.5.1" || want[pin.Family] != pin.SHA256 {
			t.Fatalf("pin = %#v, want v3.5.1 manifest", pin)
		}
	}
}

func TestFontsManifest_RejectsUnknownFamilyBeforeDownload(t *testing.T) {
	_, err := resolveNerdFontPin("Untrusted\nFamily")
	if err == nil {
		t.Fatal("unknown family accepted")
	}
	for _, want := range []string{"UntrustedFamily", "FiraCode, JetBrainsMono, Hack", "v3.5.1", "manifest", "release"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestFonts_RejectsSHA256MismatchWithoutActivation(t *testing.T) {
	if fontDigestMatches("expected", "observed") {
		t.Fatal("mismatched digest accepted")
	}
}

func TestActivateFontComponent_StagesAlongsideDestination(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create("FiraCode-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("font fixture")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	originalDownload := downloadFontFile
	t.Cleanup(func() { downloadFontFile = originalDownload })
	digest := sha256.Sum256(archive.Bytes())
	downloadFontFile = func(_ context.Context, _ *internalexec.Runner, _ string, destination string) (string, error) {
		if err := os.WriteFile(destination, archive.Bytes(), 0600); err != nil {
			return "", err
		}
		return hex.EncodeToString(digest[:]), nil
	}

	destination := filepath.Join(t.TempDir(), "Library", "Fonts", "FiraCode")
	rc := &RunContext{Runner: internalexec.NewRunner(false, slog.Default())}
	pin := fontAssetPin{Family: "FiraCode", AssetName: "FiraCode.zip", URL: "https://example.invalid/FiraCode.zip", SHA256: hex.EncodeToString(digest[:])}
	if err := activateFontComponent(context.Background(), rc, destination, pin); err != nil {
		t.Fatalf("activateFontComponent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "FiraCode-Regular.ttf")); err != nil {
		t.Fatalf("activated font unavailable: %v", err)
	}
}
