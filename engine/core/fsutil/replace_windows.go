//go:build windows

package fsutil

import (
	"errors"
	"syscall"
	"time"
)

// replaceDeadline bounds the retry: a reader holds the file for the length
// of one ReadFile, far below this.
const replaceDeadline = 2 * time.Second

const errSharingViolation syscall.Errno = 32 // ERROR_SHARING_VIOLATION

func replace(tmp, dst string, rename func(string, string) error) error {
	deadline := time.Now().Add(replaceDeadline)
	delay := time.Millisecond
	for {
		err := rename(tmp, dst)
		if err == nil || !transient(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 64*time.Millisecond {
			delay *= 2
		}
	}
}

func transient(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == syscall.ERROR_ACCESS_DENIED || errno == errSharingViolation
}
