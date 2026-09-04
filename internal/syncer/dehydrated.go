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
// bytes live only in a provider's cloud, are absent locally, and cannot be
// read back in place.
//
// Size 0 alone never qualifies. An empty file is legitimate content, and
// treating one as a placeholder would silently exempt it from the comparison
// rules the mirror plan depends on. The size check is only the cheap gate that
// keeps the xattr probe off every content-bearing file.
//
// macOS File Provider evictions (iCloud, OneDrive, Dropbox under
// ~/Library/CloudStorage) are deliberately NOT included. Such a file keeps its
// real st_size - only its allocation drops - and it faults in on read, so its
// fingerprint already describes the content. The legacy ~/Dropbox folder is
// the case that lies: a 0-byte stub whose read returns 0 bytes.
func dehydratedFile(path string, info os.FileInfo) bool {
	if info == nil || info.Size() != 0 {
		return false
	}
	_, err := unix.Getxattr(path, dropboxPlaceholderAttr, nil)
	return err == nil
}
