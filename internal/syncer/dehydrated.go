package syncer

import (
	"os"

	"golang.org/x/sys/unix"
)

// dropboxPlaceholderAttr marks a file the Dropbox client has evicted from the
// legacy ~/Dropbox folder. Such a file is a 0-byte stub on disk and reading it
// returns 0 bytes - there is no fault-in, so its size, mtime and hash all
// describe the eviction rather than the content that lives in the cloud.
const dropboxPlaceholderAttr = "com.dropbox.placeholder"

// dehydratedFile reports whether path is a cloud placeholder: a file whose
// bytes live only in a provider's cloud and are absent locally.
//
// Size 0 alone never qualifies. An empty file is legitimate content, and
// treating one as a placeholder would silently exempt it from the comparison
// rules the mirror plan depends on. The size check is only the cheap gate that
// keeps the xattr probe off every content-bearing file.
func dehydratedFile(path string, info os.FileInfo) bool {
	if info == nil || info.Size() != 0 {
		return false
	}
	if _, err := unix.Getxattr(path, dropboxPlaceholderAttr, nil); err == nil {
		return true
	}
	return datalessFlag(info)
}
