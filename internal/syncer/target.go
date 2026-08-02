package syncer

import (
	"fmt"
	"strings"
)

// TargetKind discriminates the two supported sync destinations.
type TargetKind string

const (
	// TargetLocal is a directory on this machine, typically a cloud
	// client's folder (e.g. ~/Dropbox/work) that the client uploads.
	TargetLocal TargetKind = "local"
	// TargetSSH is a remote rsync-over-SSH destination (user@host:path).
	TargetSSH TargetKind = "ssh"
)

// Target is the parsed sync destination.
type Target struct {
	Kind TargetKind
	Path string // local directory, or remote path for ssh
	Host string // user@host — ssh only
}

// ParseTarget parses a target spec:
//
//	local:~/Dropbox/work        → local directory
//	ssh:user@host:~/work        → rsync over SSH
//	~/Dropbox/work              → bare path, treated as local (back-compat)
//
// An empty spec is an error; callers decide their own default.
func ParseTarget(spec string) (Target, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Target{}, fmt.Errorf("empty target spec")
	}
	switch {
	case strings.HasPrefix(spec, "local:"):
		path := strings.TrimSpace(strings.TrimPrefix(spec, "local:"))
		if path == "" {
			return Target{}, fmt.Errorf("local target needs a path (local:~/Dropbox/work)")
		}
		return Target{Kind: TargetLocal, Path: path}, nil
	case strings.HasPrefix(spec, "ssh:"):
		rest := strings.TrimSpace(strings.TrimPrefix(spec, "ssh:"))
		host, path, ok := strings.Cut(rest, ":")
		if !ok || strings.TrimSpace(host) == "" || strings.TrimSpace(path) == "" {
			return Target{}, fmt.Errorf("ssh target must be ssh:user@host:path, got %q", spec)
		}
		return Target{Kind: TargetSSH, Host: strings.TrimSpace(host), Path: strings.TrimSpace(path)}, nil
	default:
		// Bare paths keep legacy mirror_path values working.
		return Target{Kind: TargetLocal, Path: spec}, nil
	}
}

// String renders the canonical spec form.
func (t Target) String() string {
	if t.Kind == TargetSSH {
		return "ssh:" + t.Host + ":" + t.Path
	}
	return "local:" + t.Path
}

// IsSSH reports whether the target is an SSH remote.
func (t Target) IsSSH() bool { return t.Kind == TargetSSH }

// RsyncDest returns the rsync destination argument: the local directory
// (trailing slash) or host:path for SSH.
func (t Target) RsyncDest() string {
	if t.Kind == TargetSSH {
		path := t.Path
		if !strings.HasSuffix(path, "/") {
			path += "/"
		}
		return t.Host + ":" + path
	}
	path := t.Path
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return path
}
