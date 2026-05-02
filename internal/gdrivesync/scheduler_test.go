package gdrivesync

import (
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/template"
)

func TestSchedulerState_String(t *testing.T) {
	cases := map[SchedulerState]string{
		SchedulerNotInstalled: "not installed",
		SchedulerRunning:      "running",
		SchedulerStopped:      "stopped",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("SchedulerState(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestSchedulerLabels_DistinctFromRsync(t *testing.T) {
	// Stable identifiers must not collide with internal/rsync's so that
	// both schedulers can run on the same machine. Hard-code the strings
	// so a casual rename catches in review.
	if launchdLabel != "com.dotfiles.gdrive-sync" {
		t.Errorf("launchdLabel = %q, want com.dotfiles.gdrive-sync (must differ from rsync)", launchdLabel)
	}
	if systemdTimerName != "dotfiles-gdrive-sync.timer" {
		t.Errorf("systemdTimerName = %q, want dotfiles-gdrive-sync.timer (must differ from rsync)", systemdTimerName)
	}
	if systemdServiceName != "dotfiles-gdrive-sync.service" {
		t.Errorf("systemdServiceName = %q, want dotfiles-gdrive-sync.service", systemdServiceName)
	}
	for _, label := range []string{launchdLabel, systemdTimerName, systemdServiceName} {
		if strings.Contains(label, "workspace-sync") {
			t.Errorf("label %q collides with rsync's `com.dotfiles.workspace-sync` namespace", label)
		}
	}
}

func TestPlistTemplate_RendersPushUnit(t *testing.T) {
	engine := template.NewEngine()
	data := SchedulerTemplateData{
		DotfilesPath: "/usr/local/bin/dotfiles",
		LogFile:      "/tmp/gd.log",
		Interval:     420,
		Label:        SchedulerKindPush.LaunchdLabel(),
		Action:       SchedulerKindPush.Action(),
		Description:  SchedulerKindPush.Description(),
		ServiceName:  SchedulerKindPush.SystemdServiceName(),
	}
	out, err := engine.Render("gdrivesync/com.dotfiles.gdrive-sync.plist.tmpl", data)
	if err != nil {
		t.Fatalf("render plist: %v", err)
	}
	body := string(out)
	for _, want := range []string{
		"<string>com.dotfiles.gdrive-sync</string>",
		"<string>/usr/local/bin/dotfiles</string>",
		"<string>gdrive-sync</string>",
		"<string>push</string>",
		"<integer>420</integer>",
		"<string>/tmp/gd.log</string>",
		// PATH must list Homebrew prefixes so launchd resolves rsync 3.x
		// (Apple's /usr/bin/rsync is openrsync 2.6.9 and lacks --info).
		"<key>EnvironmentVariables</key>",
		"<key>PATH</key>",
		"/opt/homebrew/bin",
		"/usr/local/bin",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered plist missing %q\n--- got ---\n%s", want, body)
		}
	}
	// Push unit must NOT contain `sync` (the deprecated alias) or `intake`.
	if strings.Contains(body, "<string>sync</string>") {
		t.Error("push plist still references deprecated `sync` action")
	}
	if strings.Contains(body, "<string>intake</string>") {
		t.Error("push plist leaked intake action")
	}
}

func TestPlistTemplate_RendersIntakeUnit(t *testing.T) {
	engine := template.NewEngine()
	data := SchedulerTemplateData{
		DotfilesPath: "/usr/local/bin/dotfiles",
		LogFile:      "/tmp/gd.log",
		Interval:     900,
		Label:        SchedulerKindIntake.LaunchdLabel(),
		Action:       SchedulerKindIntake.Action(),
		Description:  SchedulerKindIntake.Description(),
		ServiceName:  SchedulerKindIntake.SystemdServiceName(),
	}
	out, err := engine.Render("gdrivesync/com.dotfiles.gdrive-sync.plist.tmpl", data)
	if err != nil {
		t.Fatalf("render intake plist: %v", err)
	}
	body := string(out)
	for _, want := range []string{
		"<string>com.dotfiles.gdrive-sync-intake</string>",
		"<string>intake</string>",
		"<integer>900</integer>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("intake plist missing %q", want)
		}
	}
	if strings.Contains(body, "<string>push</string>") {
		t.Error("intake plist leaked push action")
	}
}

func TestSystemdTemplates_RenderPushUnit(t *testing.T) {
	engine := template.NewEngine()
	data := SchedulerTemplateData{
		DotfilesPath: "/home/u/.local/bin/dotfiles",
		LogFile:      "/home/u/.local/log/g.log",
		Interval:     900,
		Label:        SchedulerKindPush.LaunchdLabel(),
		Action:       SchedulerKindPush.Action(),
		Description:  SchedulerKindPush.Description(),
		ServiceName:  SchedulerKindPush.SystemdServiceName(),
	}

	svc, err := engine.Render("gdrivesync/dotfiles-gdrive-sync.service.tmpl", data)
	if err != nil {
		t.Fatalf("render service: %v", err)
	}
	if !strings.Contains(string(svc), "ExecStart=/home/u/.local/bin/dotfiles gdrive-sync push") {
		t.Errorf("service ExecStart wrong:\n%s", svc)
	}

	timer, err := engine.Render("gdrivesync/dotfiles-gdrive-sync.timer.tmpl", data)
	if err != nil {
		t.Fatalf("render timer: %v", err)
	}
	for _, want := range []string{
		"OnUnitActiveSec=900s",
		"Unit=dotfiles-gdrive-sync.service",
		"WantedBy=timers.target",
	} {
		if !strings.Contains(string(timer), want) {
			t.Errorf("timer missing %q\n%s", want, timer)
		}
	}
}

func TestSystemdTemplates_RenderIntakeUnit(t *testing.T) {
	engine := template.NewEngine()
	data := SchedulerTemplateData{
		DotfilesPath: "/home/u/.local/bin/dotfiles",
		LogFile:      "/home/u/.local/log/g.log",
		Interval:     900,
		Label:        SchedulerKindIntake.LaunchdLabel(),
		Action:       SchedulerKindIntake.Action(),
		Description:  SchedulerKindIntake.Description(),
		ServiceName:  SchedulerKindIntake.SystemdServiceName(),
	}

	svc, err := engine.Render("gdrivesync/dotfiles-gdrive-sync.service.tmpl", data)
	if err != nil {
		t.Fatalf("render intake service: %v", err)
	}
	if !strings.Contains(string(svc), "ExecStart=/home/u/.local/bin/dotfiles gdrive-sync intake") {
		t.Errorf("intake service ExecStart wrong:\n%s", svc)
	}

	timer, err := engine.Render("gdrivesync/dotfiles-gdrive-sync.timer.tmpl", data)
	if err != nil {
		t.Fatalf("render intake timer: %v", err)
	}
	if !strings.Contains(string(timer), "Unit=dotfiles-gdrive-sync-intake.service") {
		t.Errorf("intake timer must reference -intake service:\n%s", timer)
	}
}

func TestSchedulerKind_LabelsAreDistinct(t *testing.T) {
	if SchedulerKindPush.LaunchdLabel() == SchedulerKindIntake.LaunchdLabel() {
		t.Error("push and intake share a launchd label")
	}
	if SchedulerKindPush.SystemdTimerName() == SchedulerKindIntake.SystemdTimerName() {
		t.Error("push and intake share a systemd timer name")
	}
	if SchedulerKindPush.Action() != "push" || SchedulerKindIntake.Action() != "intake" {
		t.Errorf("Action() mismatch: push=%s intake=%s",
			SchedulerKindPush.Action(), SchedulerKindIntake.Action())
	}
}

func TestPathsFor_Kind(t *testing.T) {
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if paths.PlistFor(SchedulerKindPush) != paths.LaunchdPlist {
		t.Errorf("PlistFor(push) should equal LaunchdPlist: %q vs %q",
			paths.PlistFor(SchedulerKindPush), paths.LaunchdPlist)
	}
	intakePlist := paths.PlistFor(SchedulerKindIntake)
	if intakePlist == paths.LaunchdPlist {
		t.Error("intake plist must differ from push plist")
	}
	if !strings.HasSuffix(intakePlist, "com.dotfiles.gdrive-sync-intake.plist") {
		t.Errorf("intake plist tail wrong: %s", intakePlist)
	}
	if !strings.HasSuffix(paths.SystemdServiceFor(SchedulerKindIntake), "dotfiles-gdrive-sync-intake.service") {
		t.Errorf("intake service path wrong: %s", paths.SystemdServiceFor(SchedulerKindIntake))
	}
	if !strings.HasSuffix(paths.SystemdTimerFor(SchedulerKindIntake), "dotfiles-gdrive-sync-intake.timer") {
		t.Errorf("intake timer path wrong: %s", paths.SystemdTimerFor(SchedulerKindIntake))
	}
}

func TestResolveConfig_IntervalDefaultsAndClamps(t *testing.T) {
	t.Run("zero -> default 300", func(t *testing.T) {
		state := newIsolatedState(t)
		cfg, err := ResolveConfig(state)
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if cfg.Interval != defaultInterval {
			t.Errorf("Interval = %d, want %d", cfg.Interval, defaultInterval)
		}
	})

	t.Run("below min clamps up", func(t *testing.T) {
		// state.Validate would reject this, but ResolveConfig is a
		// downstream defense — test it directly with a hand-built state.
		state := newIsolatedState(t)
		state.Modules.GdriveSync.Interval = 5
		cfg, err := ResolveConfig(state)
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if cfg.Interval != intervalMin {
			t.Errorf("Interval = %d, want %d (clamped to min)", cfg.Interval, intervalMin)
		}
	})

	t.Run("above max clamps down", func(t *testing.T) {
		state := newIsolatedState(t)
		state.Modules.GdriveSync.Interval = 200_000
		cfg, err := ResolveConfig(state)
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if cfg.Interval != intervalMax {
			t.Errorf("Interval = %d, want %d (clamped to max)", cfg.Interval, intervalMax)
		}
	})

	t.Run("valid passes through", func(t *testing.T) {
		state := newIsolatedState(t)
		state.Modules.GdriveSync.Interval = 600
		cfg, err := ResolveConfig(state)
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if cfg.Interval != 600 {
			t.Errorf("Interval = %d, want 600", cfg.Interval)
		}
	})
}

func TestResolvePaths_IncludesSchedulerArtifacts(t *testing.T) {
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if !strings.HasSuffix(paths.LaunchdPlist, "com.dotfiles.gdrive-sync.plist") {
		t.Errorf("LaunchdPlist tail wrong: %s", paths.LaunchdPlist)
	}
	if !strings.HasSuffix(paths.SystemdService, "dotfiles-gdrive-sync.service") {
		t.Errorf("SystemdService tail wrong: %s", paths.SystemdService)
	}
	if !strings.HasSuffix(paths.SystemdTimer, "dotfiles-gdrive-sync.timer") {
		t.Errorf("SystemdTimer tail wrong: %s", paths.SystemdTimer)
	}
}
