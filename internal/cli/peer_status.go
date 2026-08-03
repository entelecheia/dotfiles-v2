package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

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

func peerBootstrapReadOnly() (*config.UserState, *syncer.Config, *exec.Runner, error) {
	state, err := config.LoadState()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading state: %w", err)
	}
	cfg, err := syncer.ResolveConfigReadOnlyForProfile(state, PeerProfile)
	if err != nil {
		return nil, nil, nil, err
	}
	enforcePeerRuntimePolicy(cfg)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return state, cfg, exec.NewRunner(false, logger), nil
}

func runPeerStatus(cmd *cobra.Command, _ []string) error {
	state, cfg, runner, err := peerBootstrapReadOnly()
	if err != nil {
		return err
	}
	st, err := syncer.GetStatus(cmd.Context(), runner, cfg, state, nil)
	if err != nil {
		return err
	}
	snapshot := inspectPeerScheduler(cmd.Context(), runner)
	base := buildSyncStatusJSON(cfg, st, &syncer.Scheduler{Paths: cfgPathsForStatus(cfg)})
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
			SchemaVersion: syncStatusSchemaVersion,
			Kind:          "peer",
			Profile:       base,
			Job:           job,
			LastExitCode:  snapshot.LastExitCode,
			RunCount:      snapshot.RunCount,
			HomePathsPath: peerHomePathsFile(cfg.LocalPaths),
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

func cfgPathsForStatus(cfg *syncer.Config) *syncer.Paths {
	paths, err := syncer.ResolvePathsForProfile(cfg.Profile)
	if err == nil {
		return paths
	}
	return &syncer.Paths{}
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

func inspectPeerScheduler(ctx context.Context, runner *exec.Runner) peerSchedulerSnapshot {
	const label = "com.dotfiles.peer"
	snapshot := peerSchedulerSnapshot{Label: label, State: syncer.SchedulerNotInstalled.String()}
	home, err := os.UserHomeDir()
	if err != nil {
		return snapshot
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	body, err := os.ReadFile(plist)
	if err != nil {
		return snapshot
	}
	snapshot.State = syncer.SchedulerStopped.String()
	snapshot.IntervalSeconds = plistInteger(string(body), "StartInterval")
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
