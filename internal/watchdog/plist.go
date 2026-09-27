package watchdog

import (
	"fmt"
	"time"
)

// ReapLabel is the launchd label of the user LaunchAgent that runs the
// reaper on the configured interval.
const ReapLabel = "com.dotfiles.watchdog.reap"

// WarpLabel is the launchd label of the root LaunchDaemon that runs the
// WARP heal pass (a daemon restart needs the system domain).
const WarpLabel = "com.dotfiles.watchdog.warp"

// ScheduledRunEnv marks a reaper run as scheduled (set by the plist, read by
// the CLI to keep scheduled runs off interactive stdout), mirroring the sync
// scheduler's DOT_SCHEDULED_RUN.
const ScheduledRunEnv = "DOT_WATCHDOG_RUN"

// RenderReapPlist renders the user LaunchAgent for the reaper. The PATH is
// explicit for the same reason the sync unit's is: launchd hands jobs a
// minimal PATH, and dot must resolve its own dependencies the same way under
// launchd as it does in a terminal.
func RenderReapPlist(dotPath string, interval time.Duration, logDir string) string {
	seconds := int(interval / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>watchdog</string>
    <string>reap</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    <key>%s</key>
    <string>1</string>
  </dict>
  <key>StartInterval</key>
  <integer>%d</integer>
  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s/reap.out.log</string>
  <key>StandardErrorPath</key>
  <string>%s/reap.err.log</string>
</dict>
</plist>
`, ReapLabel, dotPath, ScheduledRunEnv, seconds, logDir, logDir)
}

// RenderWarpPlist renders the root LaunchDaemon for the WARP heal pass.
// Same explicit-PATH rationale as the reaper unit; the daemon writes its
// stdout/stderr beside the user agent's logs.
func RenderWarpPlist(dotPath string, interval time.Duration, logDir string) string {
	seconds := int(interval / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>watchdog</string>
    <string>warp</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    <key>%s</key>
    <string>1</string>
  </dict>
  <key>StartInterval</key>
  <integer>%d</integer>
  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s/warp.out.log</string>
  <key>StandardErrorPath</key>
  <string>%s/warp.err.log</string>
</dict>
</plist>
`, WarpLabel, dotPath, ScheduledRunEnv, seconds, logDir, logDir)
}
