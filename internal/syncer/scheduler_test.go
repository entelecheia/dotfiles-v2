package syncer

import (
	"encoding/xml"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/template"
)

type plistProgramArguments struct {
	ProgramArguments []string `xml:"dict>array>string"`
}

func renderedPlistProgramArguments(t *testing.T, data SchedulerTemplateData) []string {
	t.Helper()
	body, err := template.NewEngine().Render("sync/com.dotfiles.sync.plist.tmpl", data)
	if err != nil {
		t.Fatalf("render plist: %v", err)
	}
	var plist plistProgramArguments
	if err := xml.Unmarshal(body, &plist); err != nil {
		t.Fatalf("parse plist: %v\n--- rendered ---\n%s", err, body)
	}
	return plist.ProgramArguments
}

func TestPlistHomeArgument_SupportedHome(t *testing.T) {
	home := "/tmp/a b\tline\nnext\rreturn & <tag> 'quote' \"double\" \\ % $ 유니코드/" + strings.Repeat("long-", 64)
	want := "--home=" + home

	got, err := plistHomeArgument(home)
	if err != nil {
		t.Fatalf("plistHomeArgument(%q): %v", home, err)
	}
	again, err := plistHomeArgument(home)
	if err != nil {
		t.Fatalf("second plistHomeArgument(%q): %v", home, err)
	}
	if got != again {
		t.Fatalf("serializer is not deterministic:\nfirst:  %q\nsecond: %q", got, again)
	}

	args := renderedPlistProgramArguments(t, SchedulerTemplateData{
		DotfilesPath: "/usr/local/bin/dot", Home: home, PlistHomeArg: got,
		LogFile: "/tmp/dot.log", Interval: 60, Label: launchdLabel,
		Action: "push", Mode: ModeClean.String(), Description: "test", ServiceName: systemdServiceName,
	})
	if gotArgs := matchingHomeArguments(args); !reflect.DeepEqual(gotArgs, []string{want}) {
		t.Fatalf("ProgramArguments home items = %q, want %q", gotArgs, []string{want})
	}
}

func TestPlistHomeArgument_RejectsXMLIllegalHome(t *testing.T) {
	cases := []struct {
		name string
		home string
	}{
		{name: "invalid UTF-8", home: string([]byte{'/', 't', 'm', 'p', '/', 0xff})},
		{name: "U+0001", home: "/tmp/bad\x01home"},
		{name: "U+000B", home: "/tmp/bad\x0bhome"},
		{name: "U+000C", home: "/tmp/bad\x0chome"},
		{name: "U+001F", home: "/tmp/bad\x1fhome"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := plistHomeArgument(tc.home)
			if err == nil {
				t.Fatalf("plistHomeArgument(%q) unexpectedly accepted XML-illegal home", tc.home)
			}
			for _, want := range []string{fmt.Sprintf("%q", tc.home), "XML 1.0", "rename or move", "valid UTF-8"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
		})
	}
}

func matchingHomeArguments(args []string) []string {
	var homes []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "--home=") {
			homes = append(homes, arg)
		}
	}
	return homes
}

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
	if launchdLabel != "com.dotfiles.sync" {
		t.Errorf("launchdLabel = %q, want com.dotfiles.sync (must differ from rsync)", launchdLabel)
	}
	if systemdTimerName != "dotfiles-sync.timer" {
		t.Errorf("systemdTimerName = %q, want dotfiles-sync.timer (must differ from rsync)", systemdTimerName)
	}
	if systemdServiceName != "dotfiles-sync.service" {
		t.Errorf("systemdServiceName = %q, want dotfiles-sync.service", systemdServiceName)
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
		Mode:         ModeClean.String(),
		Description:  SchedulerKindPush.Description(),
		ServiceName:  SchedulerKindPush.SystemdServiceName(),
	}
	out, err := engine.Render("sync/com.dotfiles.sync.plist.tmpl", data)
	if err != nil {
		t.Fatalf("render plist: %v", err)
	}
	body := string(out)
	for _, want := range []string{
		"<string>com.dotfiles.sync</string>",
		"<string>/usr/local/bin/dotfiles</string>",
		"<string>sync</string>",
		"<string>push</string>",
		"<string>--mode=clean</string>",
		"<integer>420</integer>",
		"<string>/tmp/gd.log</string>",
		// PATH must list Homebrew prefixes so launchd can prefer modern
		// rsync when installed, while CLI args remain Apple-rsync compatible.
		"<key>EnvironmentVariables</key>",
		"<key>PATH</key>",
		"/opt/homebrew/bin",
		"/usr/local/bin",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered plist missing %q\n--- got ---\n%s", want, body)
		}
	}
	// Push unit must NOT invoke the deprecated alias or the intake action.
	if strings.Contains(body, "<string>gsync</string>") {
		t.Error("push plist still references deprecated `gsync` command")
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
		Mode:         ModeForce.String(),
		Description:  SchedulerKindIntake.Description(),
		ServiceName:  SchedulerKindIntake.SystemdServiceName(),
	}
	out, err := engine.Render("sync/com.dotfiles.sync.plist.tmpl", data)
	if err != nil {
		t.Fatalf("render intake plist: %v", err)
	}
	body := string(out)
	for _, want := range []string{
		"<string>com.dotfiles.sync-intake</string>",
		"<string>pull</string>",
		"<string>--mode=force</string>",
		"<integer>900</integer>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("intake plist missing %q", want)
		}
	}
	if strings.Contains(body, "<string>push</string>") {
		t.Error("pull plist leaked push action")
	}
}

func TestSystemdTemplates_RenderPushUnit(t *testing.T) {
	engine := template.NewEngine()
	data := SchedulerTemplateData{
		DotfilesPath: "/home/u/.local/bin/dot",
		LogFile:      "/home/u/.local/log/g.log",
		Interval:     900,
		Label:        SchedulerKindPush.LaunchdLabel(),
		Action:       SchedulerKindPush.Action(),
		Mode:         ModeClean.String(),
		Description:  SchedulerKindPush.Description(),
		ServiceName:  SchedulerKindPush.SystemdServiceName(),
	}

	svc, err := engine.Render("sync/dotfiles-sync.service.tmpl", data)
	if err != nil {
		t.Fatalf("render service: %v", err)
	}
	if !strings.Contains(string(svc), "ExecStart=/home/u/.local/bin/dot sync push --mode=clean") {
		t.Errorf("service ExecStart wrong:\n%s", svc)
	}

	timer, err := engine.Render("sync/dotfiles-sync.timer.tmpl", data)
	if err != nil {
		t.Fatalf("render timer: %v", err)
	}
	for _, want := range []string{
		"OnUnitActiveSec=900s",
		"Unit=dotfiles-sync.service",
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
		DotfilesPath: "/home/u/.local/bin/dot",
		LogFile:      "/home/u/.local/log/g.log",
		Interval:     900,
		Label:        SchedulerKindIntake.LaunchdLabel(),
		Action:       SchedulerKindIntake.Action(),
		Mode:         ModeForce.String(),
		Description:  SchedulerKindIntake.Description(),
		ServiceName:  SchedulerKindIntake.SystemdServiceName(),
	}

	svc, err := engine.Render("sync/dotfiles-sync.service.tmpl", data)
	if err != nil {
		t.Fatalf("render intake service: %v", err)
	}
	if !strings.Contains(string(svc), "ExecStart=/home/u/.local/bin/dot sync pull --mode=force") {
		t.Errorf("pull service ExecStart wrong:\n%s", svc)
	}

	timer, err := engine.Render("sync/dotfiles-sync.timer.tmpl", data)
	if err != nil {
		t.Fatalf("render intake timer: %v", err)
	}
	if !strings.Contains(string(timer), "Unit=dotfiles-sync-intake.service") {
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
	if SchedulerKindPush.Action() != "push" || SchedulerKindIntake.Action() != "pull" {
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
	if !strings.HasSuffix(intakePlist, "com.dotfiles.sync-intake.plist") {
		t.Errorf("intake plist tail wrong: %s", intakePlist)
	}
	if !strings.HasSuffix(paths.SystemdServiceFor(SchedulerKindIntake), "dotfiles-sync-intake.service") {
		t.Errorf("intake service path wrong: %s", paths.SystemdServiceFor(SchedulerKindIntake))
	}
	if !strings.HasSuffix(paths.SystemdTimerFor(SchedulerKindIntake), "dotfiles-sync-intake.timer") {
		t.Errorf("intake timer path wrong: %s", paths.SystemdTimerFor(SchedulerKindIntake))
	}
}

func TestResolveConfig_IntervalDefaultsAndClamps(t *testing.T) {
	t.Run("zero -> off", func(t *testing.T) {
		state := newIsolatedState(t)
		cfg, err := ResolveConfig(state)
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if cfg.Interval != 0 {
			t.Errorf("Interval = %d, want 0", cfg.Interval)
		}
	})

	t.Run("below min clamps up", func(t *testing.T) {
		state := newIsolatedState(t)
		seedRawLocalConfig(t, state, "interval: 5\n")
		cfg, err := ResolveConfig(state)
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if cfg.Interval != ScheduleIntervalMin {
			t.Errorf("Interval = %d, want %d (clamped to min)", cfg.Interval, ScheduleIntervalMin)
		}
	})

	t.Run("above max clamps down", func(t *testing.T) {
		state := newIsolatedState(t)
		seedRawLocalConfig(t, state, "interval: 200000\n")
		cfg, err := ResolveConfig(state)
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if cfg.Interval != ScheduleIntervalMax {
			t.Errorf("Interval = %d, want %d (clamped to max)", cfg.Interval, ScheduleIntervalMax)
		}
	})

	t.Run("valid passes through", func(t *testing.T) {
		state := newIsolatedState(t)
		seedLocalConfig(t, state, LocalConfig{Propagation: DefaultPropagationPolicy(), Interval: 600})
		cfg, err := ResolveConfig(state)
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if cfg.Interval != 600 {
			t.Errorf("Interval = %d, want 600", cfg.Interval)
		}
	})
}

func seedLocalConfig(t *testing.T, state *config.UserState, local LocalConfig) {
	t.Helper()
	paths := ResolveLocalPaths(state.Modules.Gsync.LocalPath)
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatalf("EnsureLocalLayout: %v", err)
	}
	if local.Propagation == (PropagationPolicy{}) {
		local.Propagation = DefaultPropagationPolicy()
	}
	if err := SaveLocalConfig(paths, &local); err != nil {
		t.Fatalf("SaveLocalConfig: %v", err)
	}
}

func seedRawLocalConfig(t *testing.T, state *config.UserState, extraYAML string) {
	t.Helper()
	paths := ResolveLocalPaths(state.Modules.Gsync.LocalPath)
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatalf("EnsureLocalLayout: %v", err)
	}
	body := "propagation:\n  create: true\n  update: true\n  delete: false\n" + extraYAML
	if err := os.WriteFile(paths.ConfigFile, []byte(body), 0o644); err != nil {
		t.Fatalf("write raw local config: %v", err)
	}
}

func TestResolvePaths_IncludesSchedulerArtifacts(t *testing.T) {
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if !strings.HasSuffix(paths.LaunchdPlist, "com.dotfiles.sync.plist") {
		t.Errorf("LaunchdPlist tail wrong: %s", paths.LaunchdPlist)
	}
	if !strings.HasSuffix(paths.SystemdService, "dotfiles-sync.service") {
		t.Errorf("SystemdService tail wrong: %s", paths.SystemdService)
	}
	if !strings.HasSuffix(paths.SystemdTimer, "dotfiles-sync.timer") {
		t.Errorf("SystemdTimer tail wrong: %s", paths.SystemdTimer)
	}
}
