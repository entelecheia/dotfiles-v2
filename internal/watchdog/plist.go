package watchdog

import (
	"fmt"
	"strings"
	"time"
)

// ReapLabel is the launchd label of the user LaunchAgent that runs the
// reaper on the configured interval.
const ReapLabel = "com.dotfiles.watchdog.reap"

// xmlEscaper makes a value safe inside a plist XML <string>. Quotes and
// newlines never reach it — validPlistPath rejects those first — but an &
// or angle bracket in a path must not corrupt the document.
var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

func xmlEscape(s string) string { return xmlEscaper.Replace(s) }

// validPlistPath rejects what neither a plist XML string nor the shell
// command line inside one can carry: a double quote or a line break. Same
// rule as monit's validMonitPath, kept as the shared package-level form so
// new renderers do not grow per-unit copies.
func validPlistPath(path string) error {
	if strings.ContainsAny(path, "\"\n\r") {
		return fmt.Errorf("path %q contains a double quote or newline, which a plist string cannot carry", path)
	}
	return nil
}

// WarpLabel is the launchd label of the root LaunchDaemon that runs the
// WARP heal pass (a daemon restart needs the system domain).
const WarpLabel = "com.dotfiles.watchdog.warp"

// MonitLabel is the launchd label of the user LaunchAgent that keeps monit
// running in the foreground (launchd owns the keepalive; monit's own daemon
// mode is only its 60s check cycle).
const MonitLabel = "com.dotfiles.monit"

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
// stdout/stderr beside the user agent's logs. The daemon runs as root, so
// the arguments pin the owning user's home explicitly — without it homeFor
// resolves /var/root and the pass never finds the setup snapshot.
func RenderWarpPlist(dotPath, homeDir string, interval time.Duration, logDir string) string {
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
    <string>--home</string>
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
`, WarpLabel, dotPath, homeDir, ScheduledRunEnv, seconds, logDir, logDir)
}

// RenderMonitPlist renders the user LaunchAgent that runs monit in the
// foreground (-I) against the rendered control file. Same explicit-PATH
// rationale as the reaper unit; launchd's KeepAlive replaces monit's own
// daemon self-supervision.
func RenderMonitPlist(monitPath, monitrcPath string, logDir string) string {
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
    <string>-I</string>
    <string>-c</string>
    <string>%s</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s/monit.out.log</string>
  <key>StandardErrorPath</key>
  <string>%s/monit.err.log</string>
</dict>
</plist>
`, MonitLabel, monitPath, monitrcPath, logDir, logDir)
}
