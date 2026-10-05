package config

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestWatchdogConfig_DecodesTheIssueSketch pins the P1 schema against the
// config shape the watchdog issue specifies, durations included.
func TestWatchdogConfig_DecodesTheIssueSketch(t *testing.T) {
	doc := `
watchdog:
  enabled: true
  reaper:
    mode: enforce
    interval: 300s
    cpu_threshold: 50
    sustain: 30m
    match:
      orphan_only: true
      paths: ["$TMPDIR/**", "**/target/debug/**"]
      args: ["ahub-recovery-cli-"]
    allow: ["cloudflared", "beszel-agent", "JumpConnect"]
    kill_grace: 10s
  warp:
    enabled: true
    interval: 120s
    fail_threshold: 2
  power:
    headless: true
  monit:
    enabled: true
    load1_threshold: 12.5
    cpu_user_threshold: 80
    cpu_system_threshold: 40
    cycles: 5
    screensharing: false
  beszel:
    enabled: false
    hub_url: ""
    listen: ":45876"
    env_path: "~/.config/beszel/agent.env"
  notify:
    macos: true
    ntfy_url: "https://ntfy.example/dot"
    telegram:
      enabled: true
      env_path: "~/.config/dot/telegram.env"
      reminder_interval: 30m
      quarantine_threshold: 50
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(doc), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	w := cfg.Watchdog
	if !w.Enabled {
		t.Fatal("enabled not decoded")
	}
	if w.Reaper.Mode != "enforce" || w.Reaper.CPUThreshold != 50 {
		t.Errorf("reaper scalars = %#v", w.Reaper)
	}
	if w.Reaper.Interval.Std() != 300*time.Second {
		t.Errorf("interval = %v", w.Reaper.Interval.Std())
	}
	if w.Reaper.Sustain.Std() != 30*time.Minute {
		t.Errorf("sustain = %v", w.Reaper.Sustain.Std())
	}
	if w.Reaper.KillGrace.Std() != 10*time.Second {
		t.Errorf("kill_grace = %v", w.Reaper.KillGrace.Std())
	}
	if !w.Reaper.Match.OrphanOnly || len(w.Reaper.Match.Paths) != 2 || len(w.Reaper.Match.Args) != 1 {
		t.Errorf("match = %#v", w.Reaper.Match)
	}
	if len(w.Reaper.Allow) != 3 {
		t.Errorf("allow = %#v", w.Reaper.Allow)
	}
	if !w.Warp.Enabled || w.Warp.Interval.Std() != 120*time.Second || w.Warp.FailThreshold != 2 {
		t.Errorf("warp = %#v", w.Warp)
	}
	if !w.Power.Headless {
		t.Errorf("power = %#v", w.Power)
	}
	if !w.Monit.Enabled || w.Monit.Load1Threshold != 12.5 || w.Monit.CPUUserThreshold != 80 ||
		w.Monit.CPUSystemThreshold != 40 || w.Monit.Cycles != 5 {
		t.Errorf("monit = %#v", w.Monit)
	}
	if w.Monit.ScreenSharing == nil || *w.Monit.ScreenSharing {
		t.Errorf("monit.screensharing = %#v, want a decoded explicit false", w.Monit.ScreenSharing)
	}
	if w.Beszel.Enabled || w.Beszel.HubURL != "" {
		t.Errorf("beszel = %#v", w.Beszel)
	}
	if w.Beszel.Listen != ":45876" || w.Beszel.EnvPath != "~/.config/beszel/agent.env" {
		t.Errorf("beszel listen/env_path = %#v", w.Beszel)
	}
	var zeroBeszel WatchdogBeszelConfig
	if !zeroBeszel.IsZero() {
		t.Error("zero beszel config must report IsZero")
	}
	if (WatchdogBeszelConfig{Listen: ":1"}).IsZero() || (WatchdogBeszelConfig{EnvPath: "/tmp/x"}).IsZero() {
		t.Error("listen/env_path must defeat IsZero")
	}
	if !w.Notify.MacOS || w.Notify.NtfyURL != "https://ntfy.example/dot" {
		t.Errorf("notify = %#v", w.Notify)
	}
	if !w.Notify.Telegram.Enabled || w.Notify.Telegram.EnvPath != "~/.config/dot/telegram.env" ||
		w.Notify.Telegram.ReminderInterval.Std() != 30*time.Minute || w.Notify.Telegram.QuarantineThreshold != 50 {
		t.Errorf("notify.telegram = %#v", w.Notify.Telegram)
	}
	var zeroTelegram WatchdogTelegramConfig
	if !zeroTelegram.IsZero() {
		t.Error("zero telegram config must report IsZero")
	}
	if (WatchdogTelegramConfig{QuarantineThreshold: 10}).IsZero() ||
		(WatchdogTelegramConfig{ReminderInterval: Duration(time.Minute)}).IsZero() {
		t.Error("quarantine_threshold/reminder_interval must defeat IsZero")
	}
	var zeroNotify WatchdogNotifyConfig
	if !zeroNotify.IsZero() {
		t.Error("zero notify config must report IsZero")
	}
	if (WatchdogNotifyConfig{Telegram: WatchdogTelegramConfig{Enabled: true}}).IsZero() {
		t.Error("an enabled telegram block must defeat notify IsZero")
	}
}

// TestWatchdogNotify_EmptyTelegramOmitsFromMarshal keeps a telegram-less
// notify block rendering exactly as it did before the channel existed.
func TestWatchdogNotify_EmptyTelegramOmitsFromMarshal(t *testing.T) {
	out, err := yaml.Marshal(WatchdogNotifyConfig{MacOS: true, NtfyURL: "https://ntfy.example/dot"})
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(out), "telegram") {
		t.Errorf("empty telegram block must not render:\n%s", out)
	}
}

func TestDuration_DecodesStringsAndIntegers(t *testing.T) {
	var w WatchdogConfig
	if err := yaml.Unmarshal([]byte("reaper:\n  interval: 90s\n  sustain: 45m\n"), &w); err != nil {
		t.Fatalf("string durations: %v", err)
	}
	if w.Reaper.Interval.Std() != 90*time.Second || w.Reaper.Sustain.Std() != 45*time.Minute {
		t.Errorf("string durations = %#v", w.Reaper)
	}
	var plain WatchdogConfig
	if err := yaml.Unmarshal([]byte("reaper:\n  interval: 120\n"), &plain); err != nil {
		t.Fatalf("integer duration: %v", err)
	}
	if plain.Reaper.Interval.Std() != 120*time.Second {
		t.Errorf("integer duration read as %v (seconds expected)", plain.Reaper.Interval.Std())
	}
	var bad WatchdogConfig
	if err := yaml.Unmarshal([]byte("reaper:\n  interval: soon\n"), &bad); err == nil {
		t.Fatal("an unparseable duration must fail")
	}
}

// The setup snapshot round-trips the watchdog config through yaml.Marshal +
// Unmarshal. A duration that marshals as raw nanoseconds would decode as
// seconds and silently disable the scheduled reaper.
func TestDuration_MarshalRoundTrips(t *testing.T) {
	w := WatchdogConfig{
		Enabled: true,
		Reaper: WatchdogReaperConfig{
			Interval:  Duration(90 * time.Second),
			Sustain:   Duration(45 * time.Minute),
			KillGrace: Duration(12 * time.Second),
		},
	}
	data, err := yaml.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back WatchdogConfig
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Reaper.Interval != w.Reaper.Interval ||
		back.Reaper.Sustain != w.Reaper.Sustain ||
		back.Reaper.KillGrace != w.Reaper.KillGrace {
		t.Errorf("round trip = %#v, want %#v", back.Reaper, w.Reaper)
	}
}

// TestWatchdog_MergeIsEnableOnly mirrors the module rule: a child profile can
// enable the watchdog, but `enabled: false` does not strip a base's config.
func TestWatchdog_MergeIsEnableOnly(t *testing.T) {
	base := &Config{}
	overlay := &Config{Watchdog: WatchdogConfig{
		Enabled: true,
		Reaper:  WatchdogReaperConfig{Mode: "enforce"},
	}}
	if got := mergeConfigs(base, overlay); !got.Watchdog.Enabled || got.Watchdog.Reaper.Mode != "enforce" {
		t.Fatalf("enabling overlay lost: %#v", got.Watchdog)
	}
	disabler := &Config{Watchdog: WatchdogConfig{Enabled: false}}
	if got := mergeConfigs(overlay, disabler); !got.Watchdog.Enabled {
		t.Fatal("enabled:false overlay must not disable an inherited watchdog")
	}
}

// TestProfiles_WatchdogDisabledByDefault pins the issue's opt-in contract:
// every embedded profile must load with the watchdog off.
func TestProfiles_WatchdogDisabledByDefault(t *testing.T) {
	for _, name := range AvailableProfiles() {
		cfg, err := Load(name, "", nil)
		if err != nil {
			t.Fatalf("Load(%q): %v", name, err)
		}
		if cfg.Watchdog.Enabled {
			t.Errorf("profile %q must ship with watchdog disabled", name)
		}
	}
}

// TestWatchdog_ZeroValueOmitsFromMarshal keeps `dot config export`-style
// rendering clean when the section is unused.
func TestWatchdog_ZeroValueOmitsFromMarshal(t *testing.T) {
	out, err := yaml.Marshal(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) == "" || contains(string(out), "watchdog") {
		t.Errorf("zero watchdog must not render:\n%s", out)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
