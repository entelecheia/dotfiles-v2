package syncer

import (
	"context"
	"fmt"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// PushOptions carries what `dot sync push` needs once its cli-side guards have
// run. The propagation override is not a field: the pre-move body applied
// `--propagate` to Config before preflight, so the caller keeps doing that and
// the ordering of the two refusals is unchanged.
type PushOptions struct {
	State    *config.UserState
	Config   *Config
	Runner   *exec.Runner
	Mode     RunMode
	DryRun   bool
	Progress func(SyncEvent)
	Confirm  ConfirmFunc
}

// PushOutcome names how a push run ended; cli renders the closing line.
type PushOutcome int

const (
	PushLockBusy PushOutcome = iota // another run holds the sync lock
	PushPlanned                     // dry-run, or nothing to send: no transfer ran
	PushAborted                     // the operator declined
	PushComplete                    // the transfer ran
)

// PushCommandResult is the terminal state of a push run.
type PushCommandResult struct {
	Outcome PushOutcome
	LockErr error
}

// PushCommand runs the `dot sync push` transaction. It holds the sync lock
// across name normalization, planning, the confirmation and the transfer, so
// no second writer can interleave with any of them.
func PushCommand(ctx context.Context, opts PushOptions) (*PushCommandResult, error) {
	cfg, runner, state := opts.Config, opts.Runner, opts.State

	release, lockErr := AcquireLock(cfg.LockDir)
	if lockErr != nil {
		return &PushCommandResult{Outcome: PushLockBusy, LockErr: lockErr}, nil
	}
	defer release()

	// Once the explicit workspace migration has written its marker, every real
	// push canonicalizes newly downloaded or created NFC names before either a
	// preview plan or rsync filter is materialized. Dry-runs remain read-only.
	if !opts.DryRun {
		if err := NormalizeWorkspaceNamesBeforePush(cfg); err != nil {
			return nil, fmt.Errorf("normalizing workspace names: %w", err)
		}
		cfg.NamesNormalized = true
	}

	// SSH targets cannot be plan-previewed (the remote tree is not
	// walkable); push runs rsync directly, like the retired SSH-only sync.
	if cfg.Target.IsSSH() {
		emitSync(opts.Progress, SyncEvent{Kind: SyncEventPushSSHStart})
		if opts.Mode == ModeManual && !opts.DryRun {
			confirmed, err := askSync(opts.Confirm, ConfirmRequest{Kind: ConfirmPushSSH})
			if err != nil {
				return nil, err
			}
			if !confirmed {
				return &PushCommandResult{Outcome: PushAborted}, nil
			}
		}
		pushErr := downgradePartial(opts.Progress, Push(ctx, runner, cfg, opts.DryRun))
		RecordResult(state, cfg, "push", pushErr, opts.DryRun)
		if pushErr != nil {
			return nil, fmt.Errorf("push failed: %w", pushErr)
		}
		return &PushCommandResult{Outcome: PushComplete}, nil
	}

	emitSync(opts.Progress, SyncEvent{Kind: SyncEventPushPlanStart})
	if opts.DryRun {
		emitSync(opts.Progress, SyncEvent{Kind: SyncEventDryRunNotice})
	}
	plan, err := PlanPush(cfg)
	if err != nil {
		return nil, fmt.Errorf("planning push: %w", err)
	}
	emitSync(opts.Progress, SyncEvent{Kind: SyncEventPushPlanReady, PushPlan: plan})
	if opts.DryRun || (!plan.HasChanges() && !plan.HasConflicts()) {
		RecordResult(state, cfg, "push", nil, opts.DryRun)
		if !opts.DryRun && cfg.LocalPaths != nil {
			if err := UpdateLocalState(cfg.LocalPaths, func(s *LocalState) {
				s.LastPush = time.Now().UTC()
			}); err != nil {
				return nil, fmt.Errorf("state update: %w", err)
			}
		}
		return &PushCommandResult{Outcome: PushPlanned}, nil
	}
	if opts.Mode == ModeClean && plan.HasConflicts() {
		return nil, fmt.Errorf("push refused: %d conflict(s); rerun with --mode=force to overwrite with backups", len(plan.Conflicts))
	}
	if opts.Mode == ModeManual {
		confirmed, err := askSync(opts.Confirm, ConfirmRequest{Kind: ConfirmPushPlan})
		if err != nil {
			return nil, err
		}
		if !confirmed {
			return &PushCommandResult{Outcome: PushAborted}, nil
		}
	}
	pushErr := downgradePartial(opts.Progress, Push(ctx, runner, cfg, false))
	RecordResult(state, cfg, "push", pushErr, false)
	if pushErr != nil {
		return nil, fmt.Errorf("push failed: %w", pushErr)
	}
	return &PushCommandResult{Outcome: PushComplete}, nil
}

// downgradePartial turns an rsync partial transfer (exit 23/24) into a
// progress event and a successful run. Only the classification lives here: the
// two lines the operator reads are cli's, which is why this hands the error
// out to the event rather than describing it.
func downgradePartial(progress func(SyncEvent), err error) error {
	if err == nil || !IsPartialTransfer(err) {
		return err
	}
	emitSync(progress, SyncEvent{Kind: SyncEventPartialTransfer, Err: err})
	return nil
}
