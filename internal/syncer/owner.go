package syncer

import (
	"fmt"
	"os"
	"strings"
)

// Owner guards a sync profile against having two writers.
//
// Why this exists: the cloud mirror is a single-writer channel. Each machine
// keeps its own baseline manifest and the mirror profile runs with delete
// propagation on, so two machines pushing the same mirror do not merge - they
// take turns undoing each other, and `--max-delete` is the only thing standing
// between that and mass deletion. This was not theoretical: a second Mac came
// up with the same scheduled push installed and immediately contended for the
// same Dropbox folder.
//
// The guard is deliberately opt-in (empty Owner = unrestricted) so upgrading
// `dot` does not break a workspace that has not declared an owner yet.

// ShortHostname returns the machine's hostname without its domain suffix.
// launchd and Bonjour hand back "foo.local" in some contexts and "foo" in
// others, so compare on the short form only.
func ShortHostname() (string, error) {
	h, err := os.Hostname()
	if err != nil {
		return "", err
	}
	return NormalizeHostname(h), nil
}

// NormalizeHostname lowercases and strips the domain part.
func NormalizeHostname(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.Index(h, "."); i > 0 {
		h = h[:i]
	}
	return h
}

// OwnerMismatchError reports that this machine may not write the profile.
type OwnerMismatchError struct {
	Profile string
	Owner   string
	Host    string
}

func (e *OwnerMismatchError) Error() string {
	return fmt.Sprintf(
		"profile %q is owned by %q but this machine is %q; refusing to write.\n"+
			"  Two writers on one target corrupt it: each machine keeps its own baseline and\n"+
			"  delete propagation would make the runs undo each other.\n"+
			"  To move ownership here: dot sync owner --profile=%s --set-self\n"+
			"  To read instead of write: dot sync pull --profile=%s",
		e.Profile, e.Owner, e.Host, e.Profile, e.Profile)
}

// CheckOwner returns an OwnerMismatchError when cfg declares an owner that is
// not this machine. A nil error means writing is allowed.
func CheckOwner(cfg *Config) error {
	if cfg == nil || strings.TrimSpace(cfg.Owner) == "" {
		return nil
	}
	host, err := ShortHostname()
	if err != nil {
		// Cannot prove we are the owner, so do not claim to be.
		return fmt.Errorf("cannot determine hostname to check profile owner: %w", err)
	}
	if NormalizeHostname(cfg.Owner) == host {
		return nil
	}
	return &OwnerMismatchError{
		Profile: NormalizeProfile(cfg.Profile),
		Owner:   cfg.Owner,
		Host:    host,
	}
}
