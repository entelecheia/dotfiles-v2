package module

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	dotexec "github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/template"
)

// Legacy configs may still carry modules.ai.skills.enabled: true. The AI
// module must ignore it: skill symlinks are owned by the Maru app, so neither
// Check nor Apply may report or touch skill targets.
func TestAIModuleIgnoresLegacySkillsConfig(t *testing.T) {
	home := t.TempDir()
	// A configured source skill with no target symlink — the old code would
	// have reported a "apply skills SSOT" change and created the symlink.
	ssot := filepath.Join(home, ".maru", "skills")
	if err := os.MkdirAll(filepath.Join(ssot, "vault-extract"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ssot, "vault-extract", "SKILL.md"), []byte("# Vault Extract\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rc := &RunContext{
		Config: &config.Config{Modules: config.ModulesConfig{AI: config.AIConfig{
			Enabled: true,
			Skills: config.AISkillsConfig{
				Enabled:  true,
				Provider: "maru",
				Tools:    []string{"claude"},
			},
		}}},
		Runner:   dotexec.NewRunner(false, slog.Default()),
		Template: template.NewEngine(),
		HomeDir:  home,
	}

	res, err := (&AIModule{}).Check(context.Background(), rc)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, c := range res.Changes {
		if strings.Contains(c.Description, "skills") || strings.Contains(c.Command, "skills") {
			t.Errorf("Check reported a skills change despite diagnose-only policy: %+v", c)
		}
	}

	if _, err := (&AIModule{}).Apply(context.Background(), rc); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "vault-extract")); !os.IsNotExist(err) {
		t.Fatalf("Apply created a skill symlink despite diagnose-only policy, stat err=%v", err)
	}
}
