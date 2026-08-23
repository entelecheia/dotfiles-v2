package secrets

import (
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// StatusOptions controls Status. StoreDir arrives resolved so a caller
// honoring a --home override reports on the store that override names.
type StatusOptions struct {
	Runner   *exec.Runner
	State    *config.UserState
	Home     string
	StoreDir string
}

// FileStatus is one presence probe. Label is what the report names the row
// by: the entry label for plaintext files, the archive name for encrypted
// ones.
type FileStatus struct {
	Label   string
	Path    string
	Present bool
}

// StatusResult reports what Status observed. Recipients and the last-backup
// record are not repeated here: they are the caller's own state.
type StatusResult struct {
	Plaintext   []FileStatus
	Encrypted   []FileStatus
	AgeIdentity string
}

// Status probes every managed secret for the presence of its plaintext file
// and its encrypted archive. Absence is a field, never an error — a missing
// file is exactly what the report exists to show.
func Status(opts StatusOptions) (*StatusResult, error) {
	entries, err := Entries(opts.State, opts.Home)
	if err != nil {
		return nil, err
	}
	res := &StatusResult{
		Plaintext:   make([]FileStatus, 0, len(entries)),
		Encrypted:   make([]FileStatus, 0, len(entries)),
		AgeIdentity: ResolveAgeIdentity(opts.State, opts.Home),
	}
	for _, entry := range entries {
		res.Plaintext = append(res.Plaintext, FileStatus{
			Label:   entry.Label,
			Path:    entry.Plain,
			Present: opts.Runner.FileExists(entry.Plain),
		})
	}
	for _, entry := range entries {
		archive := filepath.Join(opts.StoreDir, entry.AgeName)
		res.Encrypted = append(res.Encrypted, FileStatus{
			Label:   entry.AgeName,
			Path:    archive,
			Present: opts.Runner.FileExists(archive),
		})
	}
	return res, nil
}
