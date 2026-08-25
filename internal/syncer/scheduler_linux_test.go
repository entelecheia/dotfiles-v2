//go:build linux

package syncer

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/template"
)

func TestSystemdAnalyze_SpecialHome(t *testing.T) {
	tool, err := osexec.LookPath("systemd-analyze")
	if err != nil {
		t.Fatalf("systemd-analyze is required for Linux scheduler syntax evidence: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := "/tmp/dotfiles scheduler % $ ' \" \\ 유니코드/" + strings.Repeat("long-", 32)
	body, err := template.NewEngine().Render("sync/dotfiles-sync.service.tmpl", SchedulerTemplateData{
		DotfilesPath: exe, Home: home, SystemdHomeArg: systemdHomeArgument(home),
		LogFile: "/tmp/dotfiles.log", Interval: 60, Label: launchdLabel,
		Action: "push", Mode: ModeClean.String(), Description: "scheduler parser test", ServiceName: systemdServiceName,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), systemdServiceName)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := osexec.Command(tool, "verify", path).CombinedOutput(); err != nil {
		t.Fatalf("systemd-analyze rejected rendered scheduler service: %v\n%s", err, output)
	}
}
