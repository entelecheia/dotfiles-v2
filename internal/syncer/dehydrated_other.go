//go:build !darwin

package syncer

import "os"

// Only macOS records dataless state in the stat flags; elsewhere a provider
// that evicts content has to be recognized by its own marker.
func datalessFlag(os.FileInfo) bool { return false }
