//go:build !windows

package fsutil

func replace(tmp, dst string, rename func(string, string) error) error {
	return rename(tmp, dst)
}
