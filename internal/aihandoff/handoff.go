// Package aihandoff shares small, explicit development notes across local agents.
// It records claims and artifact fingerprints, never independent validation.
package aihandoff

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const SummaryLimit = 32 << 10
const ArtifactLimit = 10 << 20
const logLimit = 8 << 20
const artifactCountLimit = 16
const hashBudget = 32 << 20
const historyLimit = 128
const recordLineLimit = 256 << 10

type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type Record struct {
	Agent          string     `json:"agent"`
	Kind           string     `json:"kind"`
	Time           time.Time  `json:"time"`
	HEAD           string     `json:"head"`
	Worktree       string     `json:"worktree"`
	Summary        string     `json:"summary"`
	Artifacts      []Artifact `json:"artifacts,omitempty"`
	ProducerResult string     `json:"producer_result"`
}
type Entry struct {
	Record  Record   `json:"record"`
	State   string   `json:"state"`
	Reasons []string `json:"reasons,omitempty"`
}
type Report struct {
	Omitted      int     `json:"omitted,omitempty"`
	Repository   string  `json:"repository"`
	HEAD         string  `json:"head"`
	Entries      []Entry `json:"entries"`
	Verification string  `json:"verification"`
}
type Options struct {
	Home           string
	Project        string
	Agent          string
	Kind           string
	Summary        string
	Artifacts      []string
	Result         string
	SelectedAgents []string
	DryRun         bool
}
type repository struct{ root, common, head string }

func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	argv := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	// Inherited git plumbing variables must not redirect the requested project.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git repository query failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
func resolve(ctx context.Context, project string) (repository, error) {
	root, err := git(ctx, project, "rev-parse", "--show-toplevel")
	if err != nil {
		return repository{}, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return repository{}, err
	}
	common, err := git(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return repository{}, err
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return repository{}, err
	}
	head, err := git(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return repository{}, fmt.Errorf("handoffs require a committed repository: %w", err)
	}
	return repository{root, common, head}, nil
}
func storage(home string, repo repository) (string, error) {
	if !filepath.IsAbs(home) {
		return "", errors.New("handoff home must be absolute")
	}
	hash := sha256.Sum256([]byte(repo.common))
	return filepath.Join(home, ".local", "share", "dotfiles", "ai", "handoffs", hex.EncodeToString(hash[:])+".jsonl"), nil
}

func fingerprint(root, path string, budget *int64) (Artifact, error) {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") {
		return Artifact{}, errors.New("artifact must be a repository-relative path")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return Artifact{}, errors.New("artifact escapes repository")
	}
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		lower := strings.ToLower(part)
		if lower == "target" || lower == "node_modules" || lower == ".git" || strings.Contains(lower, "secret") || strings.HasPrefix(lower, ".env") || strings.Contains(lower, "credential") || lower == "auth.json" || lower == "credentials" || lower == "id_rsa" || lower == "id_ed25519" {
			return Artifact{}, fmt.Errorf("artifact component %q is excluded", part)
		}
	}
	joined := filepath.Join(root, clean)
	actual, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return Artifact{}, err
	}
	rel, err := filepath.Rel(root, actual)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Artifact{}, errors.New("artifact symlink escapes repository")
	}
	// Symlinks are deliberately rejected so parent retargeting cannot change the
	// meaning of an existing handoff path between verification and open.
	if actual != joined {
		return Artifact{}, errors.New("artifact symlinks are not supported")
	}
	f, err := openArtifact(root, clean)
	if err != nil {
		return Artifact{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Artifact{}, err
	}
	if !st.Mode().IsRegular() || st.Size() > ArtifactLimit {
		return Artifact{}, errors.New("artifact must be a regular file at most 10 MiB")
	}
	if st.Size() > *budget {
		return Artifact{}, errors.New("artifact verification budget exhausted (32 MiB)")
	}
	h := sha256.New()
	remaining := *budget
	n, err := io.Copy(h, io.LimitReader(f, min(int64(ArtifactLimit), remaining)+1))
	*budget -= n
	if err != nil {
		return Artifact{}, err
	}
	if n > remaining {
		return Artifact{}, errors.New("artifact verification budget exhausted (32 MiB)")
	}
	after, statErr := f.Stat()
	if statErr != nil {
		return Artifact{}, statErr
	}
	if n != st.Size() || after.Size() != st.Size() || !after.ModTime().Equal(st.ModTime()) {
		return Artifact{}, errors.New("artifact changed while fingerprinting")
	}
	if n > ArtifactLimit {
		return Artifact{}, errors.New("artifact exceeds 10 MiB")
	}
	return Artifact{Path: filepath.ToSlash(clean), SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func RecordNote(ctx context.Context, o Options) (Record, error) {
	var record Record
	if err := ctx.Err(); err != nil {
		return record, err
	}
	if len(o.Summary) == 0 || len(o.Summary) > SummaryLimit || !utf8.ValidString(o.Summary) {
		return record, errors.New("summary must be valid UTF-8, nonempty, and at most 32 KiB")
	}
	switch o.Agent {
	case "claude", "codex", "kimi", "qwen", "grok", "opencode":
	default:
		return record, errors.New("unsupported producer agent")
	}
	selected := false
	for _, agent := range o.SelectedAgents {
		if agent == o.Agent {
			selected = true
		}
	}
	if !selected {
		return record, fmt.Errorf("agent %q is not selected", o.Agent)
	}
	switch o.Kind {
	case "plan", "progress", "review", "validation", "learning":
	default:
		return record, errors.New("kind must be plan, progress, review, validation, or learning")
	}
	if o.Result == "" {
		o.Result = "unverified"
	}
	switch o.Result {
	case "unverified", "passed", "failed":
	default:
		return record, errors.New("result must be unverified, passed, or failed")
	}
	if len(o.Artifacts) > artifactCountLimit {
		return record, errors.New("at most 16 artifacts per handoff")
	}
	repo, err := resolve(ctx, o.Project)
	if err != nil {
		return record, err
	}
	record = Record{Agent: o.Agent, Kind: o.Kind, Time: time.Now().UTC(), HEAD: repo.head, Worktree: repo.root, Summary: o.Summary, ProducerResult: o.Result}
	seen := map[string]bool{}
	budget := int64(hashBudget)
	for _, path := range o.Artifacts {
		if err := ctx.Err(); err != nil {
			return Record{}, err
		}
		artifact, err := fingerprint(repo.root, path, &budget)
		if err != nil {
			return Record{}, fmt.Errorf("artifact %q: %w", path, err)
		}
		if !seen[artifact.Path] {
			record.Artifacts = append(record.Artifacts, artifact)
			seen[artifact.Path] = true
		}
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	path, err := storage(o.Home, repo)
	if err != nil {
		return Record{}, err
	}
	if o.DryRun {
		return record, nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return Record{}, err
	}
	if len(data)+1 >= recordLineLimit {
		return Record{}, errors.New("encoded handoff record exceeds 256 KiB")
	}
	if err := appendLocked(ctx, o.Home, path, append(data, '\n')); err != nil {
		return Record{}, fmt.Errorf("append handoff: %w", err)
	}
	return record, nil
}

func appendLocked(ctx context.Context, home, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := openStore(ctx, home, path, true)
	if err != nil {
		return err
	}
	defer f.Close()
	fd := int(f.Fd())

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("handoff log busy")
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("handoff log is not a regular file")
	}
	if st.Size()+int64(len(data)) > logLimit {
		return errors.New("handoff log reached 8 MiB; archive it before recording more")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	n, err := f.Write(data)
	if err != nil || n != len(data) {
		_ = f.Truncate(st.Size())
		if err != nil {
			return err
		}
		return io.ErrShortWrite
	}
	return f.Sync()
}

func Show(ctx context.Context, home, project string) (Report, error) {
	repo, err := resolve(ctx, project)
	if err != nil {
		return Report{}, err
	}
	report := Report{Repository: repo.common, HEAD: repo.head, Entries: []Entry{}, Verification: "Producer results are claims; current fingerprints do not independently validate a pass."}
	path, err := storage(home, repo)
	if err != nil {
		return report, err
	}
	f, err := openStore(ctx, home, path, false)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	defer f.Close()
	fd := int(f.Fd())

	if err := syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return report, errors.New("handoff log busy; retry later")
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	st, err := f.Stat()
	if err != nil {
		return report, err
	}
	if !st.Mode().IsRegular() || st.Size() > logLimit {
		return report, errors.New("handoff log is not a bounded regular file")
	}
	scan := bufio.NewScanner(io.LimitReader(f, logLimit+1))
	scan.Buffer(make([]byte, 4096), recordLineLimit)

	var records []Record
	for scan.Scan() {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		var record Record
		if err := json.Unmarshal(scan.Bytes(), &record); err != nil {
			return report, fmt.Errorf("invalid handoff record: %w", err)
		}
		if len(record.Artifacts) > artifactCountLimit || len(record.Summary) > SummaryLimit {
			return report, errors.New("handoff record exceeds limits")
		}
		if len(records) == historyLimit {
			copy(records, records[1:])
			records = records[:historyLimit-1]
			report.Omitted++
		}
		records = append(records, record)
	}
	if err := scan.Err(); err != nil {
		return report, err
	}
	budget := int64(hashBudget)
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		entry := Entry{Record: record, State: "current"}
		if record.HEAD != repo.head {
			entry.Reasons = append(entry.Reasons, "HEAD changed")
		}
		for _, artifact := range record.Artifacts {
			current, err := fingerprint(repo.root, artifact.Path, &budget)
			if err != nil {
				entry.Reasons = append(entry.Reasons, "artifact unavailable or verification deferred: "+artifact.Path+": "+err.Error())
			} else if current.SHA256 != artifact.SHA256 {
				entry.Reasons = append(entry.Reasons, "artifact changed: "+artifact.Path)
			}
		}
		if len(entry.Reasons) > 0 {
			entry.State = "needs-revalidation"
		}
		report.Entries = append(report.Entries, entry)
	}

	return report, scan.Err()
}

// Walk directory descriptors to reject symlink swaps, not just static traversal.
func openArtifact(root, relative string) (*os.File, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), relative), nil
}

// openStore refuses symlinked storage components and never creates on a read.
func openStore(ctx context.Context, home, path string, create bool) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(canonical, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range []string{".local", "share", "dotfiles", "ai", "handoffs"} {
		if err := ctx.Err(); err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err == unix.ENOENT && create {
			if err := ctx.Err(); err != nil {
				_ = unix.Close(fd)
				return nil, err
			}
			err = unix.Mkdirat(fd, part, 0700)
			if err == nil || err == unix.EEXIST {
				next, err = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			}
		}
		_ = unix.Close(fd)
		if err != nil {
			return nil, fmt.Errorf("open handoff storage component %q: %w", part, err)
		}
		fd = next
	}
	if err := ctx.Err(); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	if create {
		flags = unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_APPEND | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	}
	// Darwin can return ENOENT for concurrent nonexclusive O_CREAT|O_NOFOLLOW
	// opens, even after the other writer created the file. Create exclusively,
	// then open an existing inode without O_CREAT; never retry a missing path.
	fileFD, err := unix.Openat(fd, filepath.Base(path), flags, 0600)
	if create && err == unix.EEXIST {
		if cancelErr := ctx.Err(); cancelErr != nil {
			_ = unix.Close(fd)
			return nil, cancelErr
		}
		fileFD, err = unix.Openat(fd, filepath.Base(path), unix.O_WRONLY|unix.O_APPEND|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	}
	_ = unix.Close(fd)
	if err != nil {
		return nil, fmt.Errorf("open handoff log: %w", err)
	}
	return os.NewFile(uintptr(fileFD), path), nil
}

// ReadSummaryFile rejects devices, pipes and symlinks before any blocking read.
func ReadSummaryFile(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > SummaryLimit {
		return "", errors.New("summary must be a regular file at most 32 KiB")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, SummaryLimit+1))
	if err != nil {
		return "", err
	}
	if len(data) > SummaryLimit {
		return "", errors.New("summary exceeds 32 KiB")
	}
	return string(data), nil
}
