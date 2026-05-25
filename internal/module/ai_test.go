package module

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	dotexec "github.com/entelecheia/dotfiles-v2/internal/exec"
)

func TestAnchorDoctorWarning(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		script     string
		wantSubstr string
	}{
		{
			name:     "missing anchor cli",
			provider: "anchor",
		},
		{
			name:     "path provider skips anchor doctor",
			provider: "path",
			script:   anchorScript("critical duplicate_source", 1, true),
		},
		{
			name:     "missing doctor subcommand skips",
			provider: "anchor",
			script: `#!/bin/sh
echo "unknown command" >&2
exit 1
`,
		},
		{
			name:     "clean doctor skips",
			provider: "anchor",
			script:   anchorScript("", 0, true),
		},
		{
			name:       "critical doctor output warns",
			provider:   "anchor",
			script:     anchorScript("critical duplicate_source", 1, true),
			wantSubstr: "critical duplicate_source",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin := t.TempDir()
			if tt.script != "" {
				writeExecutable(t, filepath.Join(bin, "anchor"), tt.script)
			}
			t.Setenv("PATH", bin)

			rc := &RunContext{
				Config: &config.Config{Modules: config.ModulesConfig{AI: config.AIConfig{
					Skills: config.AISkillsConfig{
						Enabled:  true,
						Provider: tt.provider,
						Tools:    []string{"claude"},
					},
				}}},
				Runner:  dotexec.NewRunner(false, slog.Default()),
				HomeDir: t.TempDir(),
			}

			got := (&AIModule{}).anchorDoctorWarning(context.Background(), rc)
			if tt.wantSubstr == "" {
				if got != "" {
					t.Fatalf("anchorDoctorWarning = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tt.wantSubstr) {
				t.Fatalf("anchorDoctorWarning = %q, want substring %q", got, tt.wantSubstr)
			}
		})
	}
}

func anchorScript(quietStderr string, quietExit int, helpOK bool) string {
	helpExit := 1
	if helpOK {
		helpExit = 0
	}
	return "#!/bin/sh\n" +
		"if [ \"$1\" = \"doctor\" ] && [ \"$2\" = \"--help\" ]; then exit " + strconv.Itoa(helpExit) + "; fi\n" +
		"if [ \"$1\" = \"doctor\" ] && [ \"$2\" = \"--quiet\" ]; then echo \"" + quietStderr + "\" >&2; exit " + strconv.Itoa(quietExit) + "; fi\n" +
		"exit 2\n"
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
