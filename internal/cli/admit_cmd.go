package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/admission"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/watchdog"
)

// ExitDeferred is EX_TEMPFAIL: the admission gate or the slot wait deferred
// the job, and retrying later is the correct response.
const ExitDeferred = 75

// ExitCodeError lets a command choose its process exit code; cmd/dot/main.go
// honors it via errors.As.
type ExitCodeError struct {
	Code int
	Err  error
}

func (e *ExitCodeError) Error() string { return e.Err.Error() }
func (e *ExitCodeError) Unwrap() error { return e.Err }
func (e *ExitCodeError) ExitCode() int { return e.Code }

// admitNewMonitor builds the pressure monitor the gate probes through. It is
// the fixture seam: the JSON golden fixtures substitute a monitor with a
// fixed clock and deterministic pressure evidence so the goldens stay
// byte-stable, the same role the ForGOOS seams play for the watchdog flows.
var admitNewMonitor = func(runner *exec.Runner, home string) *admission.Monitor {
	return &admission.Monitor{Runner: runner, Home: home}
}

// admitFindUncovered is the seam for the heavy-work scan: tests substitute a
// fixed inventory instead of the real process table.
var admitFindUncovered = admission.FindUncovered

func newAdmitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admit [--class heavy|maintenance] [--wait 30m] [--json] -- <command> [args...]",
		Short: "Run one heavy job per repo behind the host-pressure gate",
		Long: `Admit a heavy job (build, full test run, indexing, bulk copy, dependency
update) through the resource-admission controller. The gate defers while the
host shows memory pressure warning/critical, verifiable thermal pressure, a
WindowServer watchdog termination inside its grace window, or CPU/load
saturation sustained past the policy thresholds; recovery requires five
continuous minutes of normal telemetry. One heavy slot is held per project
repo (shared across its worktrees, branches, and sessions); different repos
run in parallel. --class maintenance takes the single host-wide maintenance
slot instead. On defer the exit code is 75 (EX_TEMPFAIL) with a
machine-readable outcome on stdout when --json is set. When a command runs,
its own stdout is the payload and the --json completion record goes to
stderr, so the exit code is the machine-readable result. 'dot ai run' and
the tooling updates share these slots. Heavy work launched outside both
holds no lease; a bounded process-table scan defers when such work runs in
this repo or its repo is unknown. Use '--' before commands that collide with
subcommand names.`,
		Args:         cobra.MinimumNArgs(1),
		RunE:         runAdmit,
		SilenceUsage: true,
	}
	cmd.Flags().String("class", admission.ClassHeavy, "slot class: heavy (per-repo) or maintenance (host-wide)")
	cmd.Flags().Duration("wait", 30*time.Minute, "maximum time to wait for the slot before deferring")
	cmd.Flags().Bool("json", false, "print the outcome as JSON")
	cmd.AddCommand(newAdmitStatusCmd())
	return cmd
}

func runAdmit(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	p := printerFrom(cmd)
	class, _ := cmd.Flags().GetString("class")
	wait, _ := cmd.Flags().GetDuration("wait")
	asJSON, _ := cmd.Flags().GetBool("json")
	if class != admission.ClassHeavy && class != admission.ClassMaintenance {
		return fmt.Errorf("--class must be %q or %q, got %q", admission.ClassHeavy, admission.ClassMaintenance, class)
	}
	if wait < 0 {
		return fmt.Errorf("--wait must not be negative")
	}
	home := homeFor(cmd)
	runner := watchdogRunner(false)
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := admission.UserStateRoot()
	if err != nil {
		return err
	}
	store := admission.NewStore(root, runner)
	scope, err := admission.ResolveScope(ctx, runner, cwd)
	if err != nil {
		return err
	}
	effective := scope
	if class == admission.ClassMaintenance {
		effective = admission.MaintenanceScope
	}

	// A child of an admitted job for the same scope and class runs without
	// re-acquiring the slot its parent already holds.
	if admission.NestedScope(os.Getenv(admission.NestedEnv), effective, class) {
		return runAdmittedChild(ctx, p, runner, args, effective, class, nil, asJSON)
	}

	monitor := admitNewMonitor(runner, home)
	decision, err := store.Gate(ctx, monitor, admission.DefaultThresholds())
	if err != nil {
		return err
	}
	if !decision.Admit {
		notifyDefer(ctx, cmd, store, effective, class, decision)
		return deferExit(p, effective, class, "", decision, asJSON)
	}
	// Heavy work that holds no slot is invisible to the leases; a scan that
	// fails or finds work in this repository (or of unknown ownership) defers.
	if jobs, uerr := admitFindUncovered(ctx, cwd, class == admission.ClassMaintenance); uerr != nil || len(jobs) > 0 {
		reason := "heavy-work inventory unavailable"
		if uerr == nil {
			reason = "uncovered heavyweight work: " + strings.Join(jobs, ", ")
		}
		d := admission.Decision{Admit: false, Reasons: []string{reason}, RetryAfter: 30 * time.Second}
		return deferExit(p, effective, class, "", d, asJSON)
	}

	lease, err := admission.SelfLease(ctx, runner, cwd)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(wait)
	backoff := 2 * time.Second
	var slot *admission.Slot
	for {
		got, holder, aerr := store.Acquire(ctx, effective, class, lease)
		if aerr != nil {
			return aerr
		}
		if got != nil {
			slot = got
			break
		}
		if wait == 0 || time.Now().Add(backoff).After(deadline) {
			owner := ""
			reason := "slot busy"
			if holder != nil {
				owner = holder.Owner
				reason = fmt.Sprintf("slot busy: %s class held by %s (pid %d, since %s)",
					class, holder.Owner, holder.PID, holder.AcquiredAt.Format(time.RFC3339))
			}
			d := admission.Decision{Admit: false, Reasons: []string{reason}, RetryAfter: backoff}
			return deferExit(p, effective, class, owner, d, asJSON)
		}
		time.Sleep(backoff)
		backoff = minDuration(backoff*3/2, 15*time.Second)
	}
	defer func() { _ = slot.Release() }()

	// The wait may have been long; re-gate before spending the slot on work
	// that pressure would now defer anyway.
	if wait > 0 {
		decision, err = store.Gate(ctx, monitor, admission.DefaultThresholds())
		if err != nil {
			return err
		}
		if !decision.Admit {
			notifyDefer(ctx, cmd, store, effective, class, decision)
			return deferExit(p, effective, class, "", decision, asJSON)
		}
	}

	return runAdmittedChild(ctx, p, runner, args, effective, class, slot, asJSON)
}

// deferExit prints the defer outcome (JSON when asked) and returns the
// EX_TEMPFAIL error main turns into exit 75.
func deferExit(p *Printer, scope, class, owner string, d admission.Decision, asJSON bool) error {
	reason := strings.Join(d.Reasons, "; ")
	outcome := admission.DeferOutcome{
		Outcome:           "deferred",
		Scope:             scope,
		Class:             class,
		Owner:             owner,
		Reason:            reason,
		RetryAfterSeconds: int64(d.RetryAfter / time.Second),
	}
	if asJSON {
		data, err := json.MarshalIndent(outcome, "", "  ")
		if err != nil {
			return err
		}
		p.Line("%s", data)
	} else {
		p.Warn("admission deferred (scope %s, class %s)", scope, class)
		p.KV("Reason", reason)
		if owner != "" {
			p.KV("Owner", owner)
		}
		p.KV("Retry after", d.RetryAfter.Round(time.Second).String())
	}
	return &ExitCodeError{Code: ExitDeferred, Err: fmt.Errorf("admission deferred: %s", reason)}
}

// notifyDefer sends at most one alert per defer episode per scope through
// the watchdog notifier. The claim is atomic (O_EXCL per-episode file), so
// concurrent defer handlers cannot duplicate the alert; a failed send
// releases the claim so the next defer retries. Best-effort: a host
// without watchdog configuration simply gets no alert.
func notifyDefer(ctx context.Context, cmd *cobra.Command, store *admission.Store, scope, class string, d admission.Decision) {
	claimed, claimPath, err := store.ClaimNotify(scope, class, d.Next.DeferSince)
	if err != nil || !claimed {
		return
	}
	mgr := watchdog.NewManager(watchdogRunner(false), homeFor(cmd))
	wcfg, err := loadWatchdogSnapshot(mgr)
	if err != nil {
		_ = os.Remove(claimPath)
		return
	}
	notifier := watchdog.NewNotifier(watchdog.ResolveNotify(wcfg.Notify), watchdogRunner(false), runtime.GOOS)
	msg := fmt.Sprintf("heavy job deferred for %s: %s", scope, strings.Join(d.Reasons, "; "))
	if err := notifier.Notify(ctx, "warn", msg); err != nil {
		_ = os.Remove(claimPath)
	}
}
