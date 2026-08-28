package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

const syncStatusSchemaVersion = 2

type syncTargetJSON struct {
	Kind string `json:"kind"`
	Spec string `json:"spec"`
	Host string `json:"host,omitempty"`
	Path string `json:"path"`
}

type syncPropagationJSON struct {
	Create bool `json:"create"`
	Update bool `json:"update"`
	Delete bool `json:"delete"`
}

type syncSensitiveOverrideJSON struct {
	AllowPattern string `json:"allowPattern"`
	DenyPattern  string `json:"denyPattern"`
}

type syncJobJSON struct {
	ID              string  `json:"id"`
	Action          string  `json:"action"`
	Label           string  `json:"label"`
	IntervalSeconds int     `json:"intervalSeconds"`
	Mode            string  `json:"mode"`
	State           string  `json:"state"`
	LastRunAt       *string `json:"lastRunAt"`
}

type syncStatusJSON struct {
	SchemaVersion      int                         `json:"schemaVersion"`
	Kind               string                      `json:"kind"`
	Profile            string                      `json:"profile"`
	Configured         bool                        `json:"configured"`
	WorkspacePath      string                      `json:"workspacePath"`
	StoreDir           string                      `json:"storeDir"`
	Target             syncTargetJSON              `json:"target"`
	LocalExists        bool                        `json:"localExists"`
	TargetExists       bool                        `json:"targetExists"`
	Paused             bool                        `json:"paused"`
	LockHeld           bool                        `json:"lockHeld"`
	Owner              string                      `json:"owner,omitempty"`
	CanPush            bool                        `json:"canPush"`
	MachineNames       []string                    `json:"machineNames"`
	FilterMode         string                      `json:"filterMode"`
	AllowCount         int                         `json:"allowCount"`
	SensitiveOverrides []syncSensitiveOverrideJSON `json:"sensitiveOverrides"`
	SubmoduleCount     int                         `json:"submoduleCount"`
	Propagation        syncPropagationJSON         `json:"propagation"`
	MaxDelete          int                         `json:"maxDelete"`
	RsyncVersion       string                      `json:"rsyncVersion,omitempty"`
	LastPullAt         *string                     `json:"lastPullAt"`
	LastPushAt         *string                     `json:"lastPushAt"`
	LastIntakeAt       *string                     `json:"lastIntakeAt"`
	ConflictCount      int                         `json:"conflictCount"`
	LogPath            string                      `json:"logPath"`
	IncludePath        string                      `json:"includePath"`
	ExcludePath        string                      `json:"excludePath"`
	IgnorePath         string                      `json:"ignorePath"`
	AllowPath          string                      `json:"allowPath"`
	Jobs               []syncJobJSON               `json:"jobs"`
}

func timeJSON(value time.Time) *string {
	if value.IsZero() {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func schedulerLabel(plist string) string {
	return strings.TrimSuffix(filepath.Base(plist), filepath.Ext(plist))
}

func buildSyncStatusJSON(cfg *syncer.Config, st *syncer.Status, sched *syncer.Scheduler) syncStatusJSON {
	configured := false
	if cfg.LocalPaths != nil {
		_, err := os.Stat(cfg.LocalPaths.ConfigFile)
		configured = err == nil
	}
	canPush := syncer.CheckOwner(cfg) == nil
	targetExists := st.MirrorExists
	if st.Target.IsSSH() {
		// Status is deliberately local-only. SSH reachability belongs to
		// `dot peer doctor`, so a configured SSH target is not marked missing
		// merely because the other laptop is asleep.
		targetExists = true
	}
	jobs := []syncJobJSON{
		{
			ID:              "mirror-push",
			Action:          "push",
			Label:           schedulerLabel(sched.Paths.PlistFor(syncer.SchedulerKindPush)),
			IntervalSeconds: st.Interval,
			Mode:            st.PushMode.String(),
			State:           st.SchedulerState.String(),
			LastRunAt:       timeJSON(st.LastPush),
		},
		{
			ID:              "mirror-pull",
			Action:          "pull",
			Label:           schedulerLabel(sched.Paths.PlistFor(syncer.SchedulerKindIntake)),
			IntervalSeconds: st.PullInterval,
			Mode:            st.PullMode.String(),
			State:           st.IntakeSchedulerState.String(),
			LastRunAt:       timeJSON(st.LastPull),
		},
	}
	overrides := make([]syncSensitiveOverrideJSON, len(st.SensitiveOverrides))
	for i, override := range st.SensitiveOverrides {
		overrides[i] = syncSensitiveOverrideJSON{
			AllowPattern: override.AllowPattern,
			DenyPattern:  override.DenyPattern,
		}
	}
	return syncStatusJSON{
		SchemaVersion:      syncStatusSchemaVersion,
		Kind:               map[bool]string{true: "peer-profile", false: "mirror"}[cfg.Profile == syncer.PeerProfile],
		Profile:            cfg.Profile,
		Configured:         configured,
		WorkspacePath:      st.LocalPath,
		StoreDir:           st.StoreDir,
		Target:             syncTargetJSON{Kind: string(st.Target.Kind), Spec: st.Target.String(), Host: st.Target.Host, Path: st.Target.Path},
		LocalExists:        st.LocalExists,
		TargetExists:       targetExists,
		Paused:             st.Paused,
		LockHeld:           st.LockHeld,
		Owner:              st.Owner,
		CanPush:            canPush,
		MachineNames:       syncer.MachineNames(),
		FilterMode:         st.FilterMode.String(),
		AllowCount:         st.AllowCount,
		SensitiveOverrides: overrides,
		SubmoduleCount:     st.SubmoduleCount,
		Propagation: syncPropagationJSON{
			Create: st.Propagation.Create,
			Update: st.Propagation.Update,
			Delete: st.Propagation.Delete,
		},
		MaxDelete:     st.MaxDelete,
		RsyncVersion:  st.RsyncVersion,
		LastPullAt:    timeJSON(st.LastPull),
		LastPushAt:    timeJSON(st.LastPush),
		LastIntakeAt:  timeJSON(st.LastIntake),
		ConflictCount: len(st.Conflicts),
		LogPath:       cfg.LogFile,
		IncludePath:   st.IncludeFile,
		ExcludePath:   st.ExcludeFile,
		IgnorePath:    st.IgnoreFile,
		AllowPath:     cfg.AllowFile,
		Jobs:          jobs,
	}
}

func writeSyncStatusJSON(cmd *cobra.Command, cfg *syncer.Config, st *syncer.Status, sched *syncer.Scheduler) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(buildSyncStatusJSON(cfg, st, sched))
}
