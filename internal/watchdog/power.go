package watchdog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Power hardening for headless hosts: capture the current pmset/systemsetup
// values before changing them, apply the headless set, and restore the
// captured values on uninstall. Command plans are pure; execution goes
// through an injected runner so tests never touch a real pmset.

// PowerState holds the pre-hardening values, persisted to power.json.
// Values are kept as strings exactly as pmset/systemsetup print them, so a
// restore replays what was there rather than a guess at its type.
type PowerState struct {
	Sleep         string `json:"sleep"`          // pmset -c sleep
	AutoRestart   string `json:"autorestart"`    // pmset -a autorestart
	Womp          string `json:"womp"`           // pmset -a womp
	RestartFreeze string `json:"restart_freeze"` // systemsetup -getrestartfreeze: On | Off
}

// CapturePower parses `pmset -g` and `systemsetup -getrestartfreeze`
// output into the state saved BEFORE hardening.
func CapturePower(pmsetG, restartFreezeOut string) (PowerState, error) {
	var st PowerState
	var err error
	if st.Sleep, err = pmsetValue(pmsetG, "sleep"); err != nil {
		return PowerState{}, err
	}
	if st.AutoRestart, err = pmsetValue(pmsetG, "autorestart"); err != nil {
		return PowerState{}, err
	}
	if st.Womp, err = pmsetValue(pmsetG, "womp"); err != nil {
		return PowerState{}, err
	}
	st.RestartFreeze, err = parseRestartFreeze(restartFreezeOut)
	if err != nil {
		return PowerState{}, err
	}
	return st, nil
}

// pmsetValue extracts one value from `pmset -g` output (" sleep               0").
// Annotated values keep their first field: pmset prints
// "sleep 0 (sleep prevented by powerd)" when something blocks sleep, and the
// parenthetical must not make the value unreadable.
func pmsetValue(pmsetG, key string) (string, error) {
	for _, line := range strings.Split(pmsetG, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == key {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("pmset -g output has no %q value", key)
}

// parseRestartFreeze reads "Restart After Power Failure: On" /
// "Restart After Freeze: On" style output into "on"/"off" (systemsetup's
// own setter spelling).
func parseRestartFreeze(out string) (string, error) {
	_, rest, found := strings.Cut(out, ":")
	if !found {
		return "", fmt.Errorf("cannot parse systemsetup -getrestartfreeze output: %q", strings.TrimSpace(out))
	}
	switch v := strings.ToLower(strings.TrimSpace(rest)); v {
	case "on", "off":
		return v, nil
	default:
		return "", fmt.Errorf("unexpected -getrestartfreeze value %q", v)
	}
}

// PowerApplyCommands is the hardening set from the issue: never sleep on
// charger, restart after power failure, wake on network access, restart
// after a freeze.
func PowerApplyCommands() [][]string {
	return [][]string{
		{"pmset", "-c", "sleep", "0"},
		{"pmset", "-a", "autorestart", "1"},
		{"pmset", "-a", "womp", "1"},
		{"systemsetup", "-setrestartfreeze", "on"},
	}
}

// PowerRestoreCommands replays the captured pre-hardening values.
func PowerRestoreCommands(st PowerState) [][]string {
	return [][]string{
		{"pmset", "-c", "sleep", st.Sleep},
		{"pmset", "-a", "autorestart", st.AutoRestart},
		{"pmset", "-a", "womp", st.Womp},
		{"systemsetup", "-setrestartfreeze", st.RestartFreeze},
	}
}

// CommandRunner is the slice of *exec.Runner the power step needs; tests
// inject a recorder.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (*exec.Result, error)
}

// RunPowerCommands executes a command plan, each entry through sudo when
// sudo is true (pmset/systemsetup mutations need root).
func RunPowerCommands(ctx context.Context, r CommandRunner, sudo bool, cmds [][]string) error {
	for _, cmd := range cmds {
		if len(cmd) == 0 {
			continue
		}
		name, args := cmd[0], cmd[1:]
		if sudo {
			args = append([]string{name}, args...)
			name = "sudo"
		}
		if _, err := r.Run(ctx, name, args...); err != nil {
			return fmt.Errorf("running %s: %w", strings.Join(cmd, " "), err)
		}
	}
	return nil
}

// LoadPowerState reads power.json. A missing file reports exists=false so
// uninstall can tell "never hardened" from "hardened".
func LoadPowerState(path string) (PowerState, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return PowerState{}, false, nil
	}
	if err != nil {
		return PowerState{}, false, fmt.Errorf("reading power state: %w", err)
	}
	var st PowerState
	if err := json.Unmarshal(data, &st); err != nil {
		return PowerState{}, false, fmt.Errorf("parsing power state %s: %w", path, err)
	}
	return st, true, nil
}

// SavePowerState writes power.json atomically (see SaveSamples).
func SavePowerState(path string, st PowerState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding power state: %w", err)
	}
	return saveJSONAtomic(path, data)
}
