package resourceguard

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Scope identifies one repository (its common Git directory across worktrees),
// or the shared installation scope for globally managed tooling.
type Scope struct {
	Key     string `json:"key"`
	Project string `json:"project,omitempty"`
	Shared  bool   `json:"shared"`
}
type HeavyJob struct {
	PID         int    `json:"pid"`
	ParentPID   int    `json:"parent_pid"`
	Name        string `json:"name"`
	Directory   string `json:"directory,omitempty"`
	Repository  string `json:"repository,omitempty"`
	Maintenance bool   `json:"maintenance,omitempty"`
}

func (j HeavyJob) description() string {
	return fmt.Sprintf("%s(pid=%d,parent=%d,repo=%s)", j.Name, j.PID, j.ParentPID, j.Repository)
}

// ResolveScope performs bounded filesystem reads, never Git subprocesses.
func ResolveScope(opts Options) (Scope, error) {
	if opts.ScopeKey != "" {
		if opts.ScopeKey != "tooling" {
			return Scope{}, fmt.Errorf("unknown shared resource scope %q", opts.ScopeKey)
		}
		return Scope{Key: "maintenance:tooling", Shared: true}, nil
	}
	dir := opts.ProjectDir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return Scope{}, err
		}
	}
	root, err := repositoryIdentity(dir)
	if err != nil {
		return Scope{}, err
	}
	return Scope{Key: "repo:" + root, Project: root}, nil
}
func scopeDirectory(base string, s Scope) string {
	return filepath.Join(base, "scopes", fmt.Sprintf("%x", sha256.Sum256([]byte(s.Key))))
}
func repositoryIdentity(dir string) (string, error) {
	current, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for depth := 0; depth < 64; depth++ {
		gitPath := filepath.Join(current, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			if !info.IsDir() {
				raw, e := readGitPointer(gitPath)
				if e != nil {
					return "", e
				}
				line := strings.TrimSpace(string(raw))
				if !strings.HasPrefix(line, "gitdir: ") {
					return "", fmt.Errorf("invalid Git indirection at %s", gitPath)
				}
				gitPath = strings.TrimSpace(strings.TrimPrefix(line, "gitdir: "))
				if !filepath.IsAbs(gitPath) {
					gitPath = filepath.Join(current, gitPath)
				}
			}
			if raw, e := readGitPointer(filepath.Join(gitPath, "commondir")); e == nil {
				common := strings.TrimSpace(string(raw))
				if !filepath.IsAbs(common) {
					common = filepath.Join(gitPath, common)
				}
				gitPath = common
			} else if !os.IsNotExist(e) {
				return "", e
			}
			return filepath.EvalSymlinks(gitPath)
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", fmt.Errorf("repository ownership unknown for %s; use --project with a Git checkout", dir)
}
func filterUncovered(s Sample, scope Scope) Sample {
	if len(s.Jobs) == 0 {
		return s
	}
	s.Uncovered = nil
	for _, j := range s.Jobs {
		if j.Repository == "" || (!scope.Shared && j.Repository == scope.Project) || (scope.Shared && j.Maintenance) {
			s.Uncovered = append(s.Uncovered, j.description())
		}
	}
	return s
}
func resolveJobDirectories(ctx context.Context, jobs []HeavyJob) []HeavyJob {
	if runtime.GOOS == "linux" {
		for i := range jobs {
			jobs[i].Directory, _ = os.Readlink(filepath.Join("/proc", strconv.Itoa(jobs[i].PID), "cwd"))
		}
	}
	if runtime.GOOS == "darwin" && len(jobs) > 0 {
		ids := make([]string, 0, len(jobs))
		for _, j := range jobs {
			ids = append(ids, strconv.Itoa(j.PID))
		}
		// One bounded lsof invocation for at most 20 jobs; permission failures leave
		// ownership unknown and therefore block rather than assuming another repo.
		out, err := output(ctx, "/usr/sbin/lsof", "-a", "-p", strings.Join(ids, ","), "-d", "cwd", "-Fn")
		cwd := lsofDirectories(out, err)
		for i := range jobs {
			jobs[i].Directory = cwd[jobs[i].PID]
		}
	}
	cache := map[string]string{}
	for i := range jobs {
		dir := jobs[i].Directory
		if dir == "" {
			continue
		}
		repo, ok := cache[dir]
		if !ok {
			repo, _ = repositoryIdentity(dir)
			cache[dir] = repo
		}
		jobs[i].Repository = repo
	}
	return jobs
}

// Git indirection files are a single short path, never bulk data.
func readGitPointer(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8192 {
		return nil, fmt.Errorf("invalid oversized Git pointer %s", path)
	}
	return os.ReadFile(path)
}

// A failed or timed-out batch is not trustworthy even when it emitted valid
// rows before failing. Every job remains unknown until a complete observation.
func lsofDirectories(out string, probeErr error) map[int]string {
	if probeErr != nil {
		return nil
	}
	cwd := map[int]string{}
	pid := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "p") {
			pid, _ = strconv.Atoi(line[1:])
		}
		if strings.HasPrefix(line, "n") && pid > 0 && filepath.IsAbs(line[1:]) {
			cwd[pid] = line[1:]
		}
	}
	return cwd
}
