package syncer

import (
	"slices"
	"strings"
	"testing"
)

func TestSecretsFilterArgs_OrderAndContent(t *testing.T) {
	args := secretsFilterArgs([]string{"/.maru/secrets/app.token"})

	// Allow patterns (with parent-dir includes) must precede every exclude.
	wantOrder := []string{
		"--include=/.maru/",
		"--include=/.maru/secrets/",
		"--include=/.maru/secrets/app.token",
		"--include=.env.example",
		"--exclude=/.secrets",
		"--exclude=.env",
	}
	lastIdx := -1
	for _, w := range wantOrder {
		idx := slices.Index(args, w)
		if idx < 0 {
			t.Fatalf("secretsFilterArgs missing %q\nargs: %v", w, args)
		}
		if idx < lastIdx {
			t.Fatalf("order violation: %q at %d before previous at %d\nargs: %v", w, idx, lastIdx, args)
		}
		lastIdx = idx
	}
	for _, w := range []string{"--exclude=/.maru/secrets/**", "--exclude=.env.*", "--exclude=/_sys/mcp.local.json"} {
		if !slices.Contains(args, w) {
			t.Errorf("secretsFilterArgs missing %q", w)
		}
	}
}

func TestSecretsFilterArgs_CommentsAndBlanksSkipped(t *testing.T) {
	args := secretsFilterArgs([]string{"# comment", "  ", ""})
	for _, a := range args {
		if strings.Contains(a, "comment") {
			t.Errorf("comment leaked into args: %q", a)
		}
	}
}

func TestAllowParentDirs(t *testing.T) {
	got := allowParentDirs("/.maru/secrets/app.token")
	want := []string{"/.maru/", "/.maru/secrets/"}
	if !slices.Equal(got, want) {
		t.Errorf("allowParentDirs = %v, want %v", got, want)
	}
	if got := allowParentDirs(".env"); got != nil {
		t.Errorf("unanchored pattern should yield no parents, got %v", got)
	}
	// Wildcard parents stop the expansion.
	got = allowParentDirs("/a/*/c.txt")
	if !slices.Equal(got, []string{"/a/"}) {
		t.Errorf("wildcard parent expansion = %v, want [/a/]", got)
	}
}

func TestSyncFilter_SecretsDenyByDefaultAndAllowOptIn(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeExclude

	f, err := newSyncFilter(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".env", "app/.env", "app/.env.local", ".secrets/token", ".maru/secrets/k.age", "_sys/mcp.local.json"} {
		if !f.shouldSkip("", rel, false) {
			t.Errorf("secret %q not skipped by default", rel)
		}
	}
	for _, rel := range []string{".env.example", "app/.env.sample"} {
		if f.shouldSkip("", rel, false) {
			t.Errorf("env template %q should sync", rel)
		}
	}

	// Explicit allow re-includes the secret and keeps its parents traversable.
	cfg.AllowPatterns = []string{"/.maru/secrets/app.token"}
	f, err = newSyncFilter(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.shouldSkip("", ".maru/secrets/app.token", false) {
		t.Error("allowed secret still skipped")
	}
	if f.shouldSkip("", ".maru/secrets", true) || f.shouldSkip("", ".maru", true) {
		t.Error("allow parent dirs not traversable")
	}
	if !f.shouldSkip("", ".maru/secrets/other.key", false) {
		t.Error("non-allowed sibling secret leaked")
	}
}

func TestSyncFilter_DoubleStarSecretAllowMatchesAtAnyDepth(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeExclude
	cfg.AllowPatterns = []string{"**/.env", "**/.env.*"}

	f, err := newSyncFilter(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		".env",
		".env.local",
		"sites/ax/.env",
		"sites/ax/.env.local",
		"sites/ax/.vercel/.env.production.local",
	} {
		if f.shouldSkipFileOrAncestor(rel) {
			t.Errorf("double-star allowed secret %q was skipped", rel)
		}
	}
}

func TestSyncFilter_SecretInventoryBlockedAndBenignNeighbors(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeExclude
	f, err := newSyncFilter(cfg, "")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		blocked string
		benign  string
	}{
		{blocked: ".ssh/known_hosts", benign: ".ssh-notes/known_hosts"},
		{blocked: ".gnupg/private-keys-v1.d/key", benign: ".gnupg-notes/key"},
		{blocked: ".aws/credentials", benign: ".aws/credentials.example"},
		{blocked: ".config/gcloud/credentials.db", benign: ".config/gcloud/credentials.db.bak"},
		{blocked: ".config/gh/hosts.yml", benign: ".config/gh/hosts.yaml"},
		{blocked: ".docker/config.json", benign: ".docker/config.example.json"},
		{blocked: ".kube/config", benign: ".kube/config.example"},
		{blocked: ".netrc", benign: ".netrc.example"},
		{blocked: ".npmrc", benign: ".npmrc.example"},
		{blocked: ".pypirc", benign: ".pypirc.example"},
		{blocked: "credentials.json", benign: "credentials.example.json"},
		{blocked: ".terraform.d/credentials.tfrc.json", benign: ".terraform.d/credentials.tfrc.example.json"},
		{blocked: ".local/share/keyrings/login.keyring", benign: ".local/share/keyrings-notes/login.keyring"},
		{blocked: "nested/ID_RSA", benign: "nested/id_rsa.pub"},
		{blocked: "nested/ID_DSA", benign: "nested/id_dsa.pub"},
		{blocked: "nested/ID_ECDSA", benign: "nested/id_ecdsa.pub"},
		{blocked: "nested/ID_ED25519", benign: "nested/id_ed25519.pub"},
		{blocked: "nested/certificate.PEM", benign: "nested/certificate.pem.txt"},
		{blocked: "nested/private.KEY", benign: "nested/private.key.txt"},
		{blocked: "nested/identity.P12", benign: "nested/identity.p12.txt"},
		{blocked: "nested/identity.PFX", benign: "nested/identity.pfx.txt"},
		{blocked: "nested/truststore.JKS", benign: "nested/truststore.jks.txt"},
		{blocked: "nested/truststore.KEYSTORE", benign: "nested/truststore.keystore.txt"},
		{blocked: "nested/secrets.KDBX", benign: "nested/secrets.kdbx.txt"},
		{blocked: "nested/terraform.TFSTATE", benign: "nested/terraform.tfstate.txt"},
		{blocked: "nested/terraform.TFSTATE.BACKUP", benign: "nested/terraform.tfstate.backup.txt"},
		{blocked: "nested/serviceAccountKey.json", benign: "nested/serviceAccountKeys.json"},
	}

	for _, tc := range cases {
		t.Run(tc.blocked, func(t *testing.T) {
			if !f.shouldSkipFileOrAncestor(tc.blocked) {
				t.Errorf("secret %q not skipped", tc.blocked)
			}
			if f.shouldSkipFileOrAncestor(tc.benign) {
				t.Errorf("benign neighbor %q was skipped", tc.benign)
			}
		})
	}
}

func TestSyncFilter_SecretRootAnchorsDoNotMatchNestedFiles(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeExclude
	f, err := newSyncFilter(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"nested/.npmrc",
		"nested/.pypirc",
		"nested/credentials.json",
	} {
		if f.shouldSkipFileOrAncestor(rel) {
			t.Errorf("root-anchored secret rule incorrectly skipped %q", rel)
		}
	}
}

func TestSensitiveOverrides(t *testing.T) {
	got := SensitiveOverrides([]string{
		"/.maru/secrets/app.token",
		"/.aws/credentials",
		"/.aws/credentials",
	})
	want := []SensitiveOverride{
		{AllowPattern: "/.aws/credentials", DenyPattern: "/.aws/credentials"},
		{AllowPattern: "/.maru/secrets/app.token", DenyPattern: "/.maru/secrets"},
		{AllowPattern: "/.maru/secrets/app.token", DenyPattern: "/.maru/secrets/**"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("SensitiveOverrides() = %#v, want %#v", got, want)
	}
	if got := SensitiveOverrides([]string{".env.example"}); got != nil {
		t.Errorf("built-in template allow produced override findings: %#v", got)
	}
}

func TestSensitiveOverrides_ReportsWildcardIntersections(t *testing.T) {
	got := SensitiveOverrides([]string{"foo.*"})
	want := SensitiveOverride{AllowPattern: "foo.*", DenyPattern: rsyncCaseFoldPattern("*.pem")}
	if !slices.Contains(got, want) {
		t.Errorf("SensitiveOverrides() = %#v, want wildcard intersection %#v", got, want)
	}
}

func TestSecretPatternsOverlap_GlobIntersections(t *testing.T) {
	pem := rsyncCaseFoldPattern("*.pem")
	for _, tc := range []struct {
		allow string
		deny  string
		want  bool
	}{
		{allow: "foo.*", deny: pem, want: true},
		{allow: "docs/*", deny: pem, want: true},
		{allow: "foo.txt", deny: pem, want: false},
		{allow: "docs/*.txt", deny: pem, want: false},
	} {
		if got := secretPatternsOverlap(tc.allow, tc.deny); got != tc.want {
			t.Errorf("secretPatternsOverlap(%q, %q) = %v, want %v", tc.allow, tc.deny, got, tc.want)
		}
	}
}

func TestSyncFilter_ExplicitSecretAllowPreservesTransferAndReportsOverride(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeExclude
	cfg.AllowPatterns = []string{"/.aws/credentials"}
	f, err := newSyncFilter(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.shouldSkipFileOrAncestor(".aws/credentials") {
		t.Fatal("explicit allow no longer preserves transfer eligibility")
	}
	want := []SensitiveOverride{{AllowPattern: "/.aws/credentials", DenyPattern: "/.aws/credentials"}}
	if got := SensitiveOverrides(cfg.AllowPatterns); !slices.Equal(got, want) {
		t.Errorf("SensitiveOverrides() = %#v, want %#v", got, want)
	}
}

func TestSyncFilter_SubmodulesAlwaysSkipped(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeExclude
	f, err := newSyncFilter(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	f.submodules = []string{"dev", "sites/a", "vault"}
	for _, rel := range []string{"dev", "dev/maru/main.go", "sites/a/index.html", "vault/notes/x.md"} {
		if !f.shouldSkip("", rel, strings.HasSuffix(rel, "dev")) {
			t.Errorf("submodule path %q not skipped", rel)
		}
	}
	if f.shouldSkip("", "sites-notes.md", false) {
		t.Error("non-submodule sibling wrongly skipped")
	}
}

func TestSyncFilter_IncludeModeTrackedUnion(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeInclude
	cfg.IncludePatterns = []string{"*.pdf"}
	f, err := newSyncFilter(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	f.tracked = map[string]bool{"src/main.go": true, "docs/plan.md": true}

	for _, rel := range []string{"src/main.go", "docs/plan.md", "assets/scan.PDF"} {
		if f.shouldSkip("", rel, false) {
			t.Errorf("union member %q skipped", rel)
		}
	}
	if !f.shouldSkip("", "untracked-notes.md", false) {
		t.Error("untracked non-binary file must not sync in include mode")
	}
	if f.shouldSkip("", "anydir", true) {
		t.Error("directories must stay traversable in include mode")
	}
}
