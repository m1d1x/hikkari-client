package netdoctor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"NekkoVPN/core/fsutil"
)

// DisableHostsOverrides comments out the protected-domain overrides in the
// hosts file at path and returns how many it disabled. A timestamped copy of
// the file is written beside it first, and without that copy nothing changes.
// It writes nothing when there is nothing to change. The errors carry no
// path: they go to the anonymized log.
func DisableHostsOverrides(path string, now time.Time) (int, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading hosts: %w", BareFileError(err))
	}
	filtered, n := FilterHosts(content)
	if n == 0 {
		return 0, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("reading hosts: %w", BareFileError(err))
	}
	backup := fmt.Sprintf("%s.hikkari-%d.bak", path, now.Unix())
	if err := ReplaceFile(backup, content, info.Mode().Perm()); err != nil {
		return 0, fmt.Errorf("backing up hosts: %w", BareFileError(err))
	}
	if err := ReplaceFile(path, filtered, info.Mode().Perm()); err != nil {
		return 0, fmt.Errorf("writing hosts: %w", BareFileError(err))
	}
	return n, nil
}

// ReplaceFile puts content at path all at once: readers and a power cut see
// the old file or the new one, never a part. The temporary file sits beside
// path, because a rename is atomic only within a volume.
func ReplaceFile(path string, content []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hikkari-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // fails harmlessly once the rename has moved it
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	return fsutil.Replace(tmp.Name(), path)
}

// BareFileError drops the path from a file error, for the anonymized log.
func BareFileError(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return linkErr.Err
	}
	return err
}
