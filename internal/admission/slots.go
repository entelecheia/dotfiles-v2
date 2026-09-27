package admission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/watchdog"
)

// Slot classes.
const (
	// ClassHeavy is the per-repo build/test/index slot: one per scope.
	ClassHeavy = "heavy"
	// ClassMaintenance is the host-wide install/update slot: one shared
	// slot regardless of which repo the work started in.
	ClassMaintenance = "maintenance"
)

// DefaultHeartbeatInterval is how often a running job refreshes its lease.
// The stale bound is twice this: one missed heartbeat never frees a slot.
const DefaultHeartbeatInterval = 30 * time.Second

const heartbeatStaleFactor = 2

// Environment seams.
const (
	// NestedEnv marks a process as the child of an admitted job. Its value
	// is a JSON {"scope":...,"class":...} object; a child `dot admit` for
	// the same scope and class runs without re-acquiring the slot its
	// parent already holds.
	NestedEnv = "DOT_ADMISSION_SCOPE"
	// OwnerEnv overrides the lease owner label (default user@host). It is a
	// display/ownership label only; liveness is always pid + start time.
	OwnerEnv = "DOT_ADMISSION_OWNER"
	// SessionEnv carries a session identifier into the lease for status
	// visibility.
	SessionEnv = "DOT_ADMISSION_SESSION"
)

// Lease is the ownership record inside a slot directory. PID and PIDStart
// together identify the owning process incarnation (PID reuse safe, the same
// rule the watchdog reaper enforces); the deadline is heartbeat_at plus the
// stale factor and is what an owner crash actually expires.
type Lease struct {
	Owner       string    `json:"owner"`
	Session     string    `json:"session,omitempty"`
	PID         int       `json:"pid"`
	PIDStart    string    `json:"pid_start"`
	PGID        int       `json:"pgid"`
	Class       string    `json:"class"`
	Scope       string    `json:"scope"`
	CWD         string    `json:"cwd"`
	AcquiredAt  time.Time `json:"acquired_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
	Deadline    time.Time `json:"deadline"`
}

// Store owns the admission state root (~/.local/state/dot/admission/): the
// slot directories (one per class+scope), the pressure history, and the
// per-scope notify dedup marks.
type Store struct {
	Root   string
	Runner *exec.Runner // ps liveness probes
	Now    func() time.Time
	// HeartbeatInterval defaults to DefaultHeartbeatInterval when zero.
	HeartbeatInterval time.Duration
}

// DefaultStateRoot is the state root for the invoking user.
func DefaultStateRoot(home string) string {
	return filepath.Join(home, ".local", "state", "dot", "admission")
}

// NewStore builds a store rooted at root.
func NewStore(root string, runner *exec.Runner) *Store {
	return &Store{Root: root, Runner: runner}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) heartbeatInterval() time.Duration {
	if s.HeartbeatInterval > 0 {
		return s.HeartbeatInterval
	}
	return DefaultHeartbeatInterval
}

// HeartbeatStaleAfter is the lease lifetime without a heartbeat refresh.
func (s *Store) HeartbeatStaleAfter() time.Duration {
	return heartbeatStaleFactor * s.heartbeatInterval()
}

// slotDir names a slot by class plus a hash of the scope: the hash keeps
// slashes and length out of the directory name, and the scope itself is
// recorded inside the lease for status.
func (s *Store) slotDir(scope, class string) string {
	sum := sha256.Sum256([]byte(scope))
	return filepath.Join(s.Root, "slots", fmt.Sprintf("%s-%s", class, hex.EncodeToString(sum[:])[:16]))
}

// HistoryPath is the cross-invocation pressure history.
func (s *Store) HistoryPath() string {
	return filepath.Join(s.Root, "history.json")
}

// Slot is a held slot. The zero value is invalid; Acquire returns it.
type Slot struct {
	store *Store
	dir   string
	lease Lease
}

// Lease returns a copy of the lease this slot holds.
func (sl *Slot) Lease() Lease { return sl.lease }

// Acquire takes the (scope, class) slot. The acquire is a single atomic
// mkdir — there is deliberately no multi-lock ordering because there is only
// ever one lock per admission. On success it returns a Slot. When the slot
// is held it returns the holder's lease (for owner+reason visibility) and a
// nil Slot. A stale slot is reclaimed only when its heartbeat deadline has
// passed AND the owning process incarnation is verifiably gone; an
// unverifiable identity (ps failure) is busy, mirroring the watchdog rule
// that an unverifiable identity answers on the safe side.
func (s *Store) Acquire(ctx context.Context, scope, class string, lease Lease) (*Slot, *Lease, error) {
	if err := os.MkdirAll(filepath.Join(s.Root, "slots"), 0o755); err != nil {
		return nil, nil, fmt.Errorf("creating slots dir: %w", err)
	}
	dir := s.slotDir(scope, class)
	for attempt := 0; attempt < 3; attempt++ {
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			now := s.now()
			lease.Scope = scope
			lease.Class = class
			lease.AcquiredAt = now
			lease.HeartbeatAt = now
			lease.Deadline = now.Add(s.HeartbeatStaleAfter())
			if err := writeLease(dir, lease); err != nil {
				_ = os.RemoveAll(dir)
				return nil, nil, err
			}
			return &Slot{store: s, dir: dir, lease: lease}, nil, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, nil, fmt.Errorf("acquiring slot %s: %w", dir, err)
		}
		holder, stale, herr := s.slotHolder(ctx, dir)
		if herr != nil {
			return nil, nil, herr
		}
		if !stale {
			return nil, holder, nil
		}
		if err := os.RemoveAll(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("reclaiming stale slot %s: %w", dir, err)
		}
	}
	return nil, nil, fmt.Errorf("slot %s changed hands while acquiring; retry", dir)
}

// slotHolder reads the existing lease and reports whether it is stale enough
// to reclaim. A fresh heartbeat is never reclaimed regardless of what ps
// says about the pid — the owner might be between heartbeats, and the
// deadline is the only clock that can prove abandonment.
func (s *Store) slotHolder(ctx context.Context, dir string) (holder *Lease, stale bool, err error) {
	lease, lerr := readLease(dir)
	if lerr != nil {
		// A corrupt lease cannot prove ownership; it is reclaimable only
		// after the slot directory itself has gone quiet past the stale
		// bound.
		info, serr := os.Stat(dir)
		if serr != nil {
			if errors.Is(serr, fs.ErrNotExist) {
				return nil, true, nil
			}
			return nil, false, serr
		}
		if s.now().Sub(info.ModTime()) > s.HeartbeatStaleAfter() {
			return nil, true, nil
		}
		return nil, false, nil
	}
	if s.now().Before(lease.Deadline) {
		return &lease, false, nil
	}
	match, merr := watchdog.ProcessStartMatches(ctx, s.Runner, lease.PID, lease.PIDStart)
	if merr != nil {
		// Identity unverifiable: stay busy rather than reclaiming from under
		// a live owner whose ps probe failed.
		return &lease, false, nil
	}
	if match {
		// Owner alive but heartbeat stalled; freeing it would admit a second
		// heavy job next to a live one.
		return &lease, false, nil
	}
	return &lease, true, nil
}

// Heartbeat refreshes the lease deadline. If the slot changed hands (a stale
// reclaim plus re-acquire by someone else), Heartbeat refuses rather than
// overwriting the new owner's record, and the caller should stop heartbeating.
func (sl *Slot) Heartbeat() error {
	current, err := readLease(sl.dir)
	if err != nil {
		return fmt.Errorf("heartbeat lost slot %s: %w", sl.dir, err)
	}
	if !sameLease(current, sl.lease) {
		return fmt.Errorf("heartbeat lost slot %s: slot changed hands", sl.dir)
	}
	sl.lease.HeartbeatAt = sl.store.now()
	sl.lease.Deadline = sl.lease.HeartbeatAt.Add(sl.store.HeartbeatStaleAfter())
	return writeLease(sl.dir, sl.lease)
}

// Release removes the slot directory, but only while the lease inside is
// still ours: a release that lands after a reclaim plus re-acquire must not
// delete the new owner's slot.
func (sl *Slot) Release() error {
	current, err := readLease(sl.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err == nil && !sameLease(current, sl.lease) {
		return nil
	}
	if err := os.RemoveAll(sl.dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("releasing slot %s: %w", sl.dir, err)
	}
	return nil
}

func sameLease(a, b Lease) bool {
	return a.PID == b.PID && a.AcquiredAt.Equal(b.AcquiredAt)
}

func readLease(dir string) (Lease, error) {
	data, err := os.ReadFile(filepath.Join(dir, "lease.json"))
	if err != nil {
		return Lease{}, err
	}
	var lease Lease
	if err := json.Unmarshal(data, &lease); err != nil {
		return Lease{}, fmt.Errorf("parsing lease in %s: %w", dir, err)
	}
	return lease, nil
}

// writeLease persists the lease atomically (temp + rename), so a concurrent
// reader only ever sees the old or the new record, never a torn one.
func writeLease(dir string, lease Lease) error {
	data, err := json.MarshalIndent(lease, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".lease-*")
	if err != nil {
		return fmt.Errorf("creating lease temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, "lease.json"))
}

// LoadHistory reads the pressure history. A missing file is an empty
// history, not an error: the first gate evaluation has no past.
func LoadHistory(path string) (History, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return History{}, nil
	}
	if err != nil {
		return History{}, fmt.Errorf("reading history: %w", err)
	}
	var h History
	if err := json.Unmarshal(data, &h); err != nil {
		return History{}, fmt.Errorf("parsing history %s: %w", path, err)
	}
	return h, nil
}

// SaveHistory writes the history atomically.
func SaveHistory(path string, h History) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating history dir: %w", err)
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".history-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Gate runs one pressure-gate evaluation: snapshot, evaluate against the
// persisted history, persist the updated history. The load-evaluate-save
// cycle holds a short mkdir lock so concurrent invocations cannot interleave
// and silently drop one run's sustain/recovery streak. When another
// evaluation holds the lock, Gate defers conservatively rather than reading
// a half-updated history. The evaluation clock comes from the monitor when
// it carries one, matching the fixture seams.
func (s *Store) Gate(ctx context.Context, m *Monitor, th Thresholds) (Decision, error) {
	release, busy, err := acquireHistoryLock(s.Root)
	if err != nil {
		return Decision{}, fmt.Errorf("locking history: %w", err)
	}
	if busy {
		return Decision{
			Admit:      false,
			Reasons:    []string{"another admission gate evaluation is in progress"},
			RetryAfter: 10 * time.Second,
		}, nil
	}
	defer release()
	hist, err := LoadHistory(s.HistoryPath())
	if err != nil {
		return Decision{}, err
	}
	now := s.now()
	if m.Now != nil {
		now = m.Now()
	}
	d := EvaluatePressure(m.SnapshotPressure(ctx), th, hist, now)
	if err := SaveHistory(s.HistoryPath(), d.Next); err != nil {
		return Decision{}, err
	}
	return d, nil
}

// acquireHistoryLock serializes the load-evaluate-save cycle on
// history.json between concurrent `dot admit` invocations, mirroring the
// watchdog pass lock: a lock older than the bound belongs to a dead pass.
func acquireHistoryLock(root string) (release func(), busy bool, err error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, false, err
	}
	lockPath := filepath.Join(root, "history.lock")
	for attempt := 0; attempt < 2; attempt++ {
		err := os.Mkdir(lockPath, 0o755)
		if err == nil {
			return func() { _ = os.Remove(lockPath) }, false, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, false, err
		}
		info, statErr := os.Lstat(lockPath)
		if statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				continue
			}
			return nil, false, statErr
		}
		if time.Since(info.ModTime()) <= time.Minute {
			return nil, true, nil
		}
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, false, err
		}
	}
	return nil, true, nil
}

// notifyMarkPath records the defer episode a scope was last notified for.
func (s *Store) notifyMarkPath(scope, class string) string {
	sum := sha256.Sum256([]byte(scope))
	return filepath.Join(s.Root, "notify", fmt.Sprintf("%s-%s.json", class, hex.EncodeToString(sum[:])[:16]))
}

// ShouldNotify reports whether a defer in this episode (identified by its
// DeferSince) still needs an alert: at most one notification per defer
// episode per scope.
func (s *Store) ShouldNotify(scope, class string, episode time.Time) bool {
	data, err := os.ReadFile(s.notifyMarkPath(scope, class))
	if err != nil {
		return true
	}
	var mark struct {
		Episode time.Time `json:"episode"`
	}
	if json.Unmarshal(data, &mark) != nil {
		return true
	}
	return !mark.Episode.Equal(episode)
}

// MarkNotified records that this episode's alert was sent.
func (s *Store) MarkNotified(scope, class string, episode time.Time) error {
	path := s.notifyMarkPath(scope, class)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]time.Time{"episode": episode})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ListLeases returns the lease of every slot currently on disk. Bounded by
// the slot count (one per repo plus maintenance); unreadable slots are
// skipped rather than failing the whole status view.
func (s *Store) ListLeases() ([]Lease, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "slots"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing slots: %w", err)
	}
	var out []Lease
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		lease, err := readLease(filepath.Join(s.Root, "slots", e.Name()))
		if err != nil {
			continue
		}
		out = append(out, lease)
	}
	return out, nil
}

// NestedScope reports whether env (the value of DOT_ADMISSION_SCOPE) marks
// this process as the child of an admitted job for scope and class.
func NestedScope(env, scope, class string) bool {
	if env == "" {
		return false
	}
	var mark struct {
		Scope string `json:"scope"`
		Class string `json:"class"`
	}
	if json.Unmarshal([]byte(env), &mark) != nil {
		return false
	}
	return mark.Scope == scope && mark.Class == class
}

// NestedEnvValue renders the DOT_ADMISSION_SCOPE value a parent exports to
// its child.
func NestedEnvValue(scope, class string) string {
	data, _ := json.Marshal(map[string]string{"scope": scope, "class": class})
	return string(data)
}
