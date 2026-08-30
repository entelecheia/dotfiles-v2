package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

const peerStatusSchemaVersion = syncer.PeerStatusSchemaVersion
const peerSchedulerUnsupportedState = "unsupported: peer scheduler requires macOS launchd"

type peerSchedulerSnapshot struct {
	Label           string
	State           string
	IntervalSeconds int
	LastExitCode    *int
	RunCount        *int
}

type peerStatusJSON struct {
	SchemaVersion int            `json:"schemaVersion"`
	Kind          string         `json:"kind"`
	Profile       syncStatusJSON `json:"profile"`
	Job           syncJobJSON    `json:"job"`
	LastExitCode  *int           `json:"lastExitCode"`
	RunCount      *int           `json:"runCount"`
	HomePathsPath string         `json:"homePathsPath"`
}

func newPeerStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "status",
		Short:        "Show local peer profile and scheduler status",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE:         runPeerStatus,
	}
	cmd.Flags().Bool("json", false, "print a stable machine-readable status document")
	return cmd
}

// peerBootstrapReadOnly resolves the peer profile without creating its store.
// The runner is deliberately live: reading status must work under --dry-run.
//
// It takes the command because it must read --home: this is the path
// `dot peer status --json` and `dot peer home-paths get` take, and neither
// goes through peerBootstrapOptions (BUG-07).
func peerBootstrapReadOnly(cmd *cobra.Command) (*syncer.BootstrapResult, error) {
	return syncer.Bootstrap(syncer.BootstrapOptions{
		Profile:  PeerProfile,
		ReadOnly: true,
		Home:     homeOverrideFrom(cmd),
	})
}

func runPeerStatus(cmd *cobra.Command, _ []string) error {
	bs, err := peerBootstrapReadOnly(cmd)
	if err != nil {
		return err
	}
	state, cfg, runner := bs.State, bs.Config, bs.Runner
	st, err := syncer.GetStatus(cmd.Context(), runner, cfg, state, nil)
	if err != nil {
		return err
	}
	snapshot := inspectPeerScheduler(cmd.Context(), runner, homeFor(cmd), homeOverrideFrom(cmd) != "", runtime.GOOS)
	base := buildSyncStatusJSON(cfg, st, &syncer.Scheduler{Paths: cfg.SystemPaths})
	base.Kind = "peer-profile"
	base.Jobs = []syncJobJSON{}
	job := syncJobJSON{
		ID:              "peer-sync",
		Action:          "peer-sync",
		Label:           snapshot.Label,
		IntervalSeconds: snapshot.IntervalSeconds,
		Mode:            "safe-bidirectional",
		State:           snapshot.State,
		LastRunAt:       newestTimeJSON(st.LastPull, st.LastPush),
	}
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(peerStatusJSON{
			SchemaVersion: peerStatusSchemaVersion,
			Kind:          "peer",
			Profile:       base,
			Job:           job,
			LastExitCode:  snapshot.LastExitCode,
			RunCount:      snapshot.RunCount,
			HomePathsPath: syncer.PeerHomePathsFile(cfg.LocalPaths),
		})
	}
	p := printerFrom(cmd)
	p.Header("Peer Status")
	p.KV("Workspace", st.LocalPath)
	p.KV("Target", st.Target.String())
	p.KV("Scheduler", snapshot.State)
	if snapshot.IntervalSeconds > 0 {
		p.KV("Interval", formatInterval(snapshot.IntervalSeconds))
	}
	if snapshot.LastExitCode != nil {
		p.KV("Last exit", strconv.Itoa(*snapshot.LastExitCode))
	}
	p.KV("Last pull", formatLastSync(st.LastPull))
	p.KV("Last push", formatLastSync(st.LastPush))
	p.KV("Conflicts", strconv.Itoa(len(st.Conflicts)))
	return nil
}

func newestTimeJSON(values ...time.Time) *string {
	var newest time.Time
	for _, value := range values {
		if value.After(newest) {
			newest = value
		}
	}
	return timeJSON(newest)
}

// inspectPeerScheduler reads the peer agent's plist under the home the run is
// pointed at. It takes the home rather than resolving one: a --home run that
// reported the invoking user's agent state would be the same disclosure the
// workspace fields above carry (BUG-07).
func inspectPeerScheduler(ctx context.Context, runner *exec.Runner, home string, targetUserDomain bool, goos string) peerSchedulerSnapshot {
	const label = "com.dotfiles.peer"
	snapshot := peerSchedulerSnapshot{Label: label, State: syncer.SchedulerNotInstalled.String()}
	if goos != "darwin" {
		snapshot.State = peerSchedulerUnsupportedState
		return snapshot
	}
	if home == "" {
		return snapshot
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	body, err := os.ReadFile(plist)
	if err != nil {
		return snapshot
	}
	snapshot.State = syncer.SchedulerStopped.String()
	snapshot.IntervalSeconds = plistInteger(string(body), "StartInterval")
	if targetUserDomain {
		snapshot.State = syncer.SchedulerTargetUserActionRequired.String()
		return snapshot
	}
	result, err := runner.RunQuery(ctx, "launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), label))
	if err != nil || result == nil || result.ExitCode != 0 {
		return snapshot
	}
	snapshot.State = syncer.SchedulerRunning.String()
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if raw, ok := strings.CutPrefix(line, "last exit code ="); ok {
			if value, parseErr := strconv.Atoi(strings.TrimSpace(raw)); parseErr == nil {
				snapshot.LastExitCode = &value
			}
		}
		if raw, ok := strings.CutPrefix(line, "runs ="); ok {
			if value, parseErr := strconv.Atoi(strings.TrimSpace(raw)); parseErr == nil {
				snapshot.RunCount = &value
			}
		}
	}
	return snapshot
}

func plistInteger(body, key string) int {
	marker := "<key>" + key + "</key>"
	index := strings.Index(body, marker)
	if index < 0 {
		return 0
	}
	rest := body[index+len(marker):]
	start := strings.Index(rest, "<integer>")
	end := strings.Index(rest, "</integer>")
	if start < 0 || end < 0 || end <= start {
		return 0
	}
	value, _ := strconv.Atoi(strings.TrimSpace(rest[start+len("<integer>") : end]))
	return value
}
