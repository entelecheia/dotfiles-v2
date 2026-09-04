//go:build darwin

package syncer

import (
	"os"
	"syscall"
)

// ufDataless is macOS's UF_DATALESS: the File Provider marks a file whose
// content has not been materialized with it. iCloud, OneDrive and a Dropbox
// install under ~/Library/CloudStorage all present evicted files this way,
// rather than with the legacy Dropbox xattr.
const ufDataless = 0x40000000

func datalessFlag(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Flags&ufDataless != 0
}
