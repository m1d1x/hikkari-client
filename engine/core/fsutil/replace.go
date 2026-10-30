// Package fsutil holds file operations whose correctness differs by OS.
package fsutil

import "os"

// Replace atomically moves the finished temporary file tmp over dst.
//
// On Windows a rename onto a file fails with "Access is denied" or a sharing
// violation while any other handle has dst open — and the engine's
// background workers read state files (profiles.json, routing.json) at any
// moment. Those handles are short-lived, so the rename is retried briefly
// there; on other systems rename(2) replaces an open file and is tried once.
func Replace(tmp, dst string) error {
	return replace(tmp, dst, os.Rename)
}
