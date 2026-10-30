package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// unzip extracts data into dir, refusing entries that would land outside it.
func unzip(data []byte, dir string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	root := filepath.Clean(dir) + string(os.PathSeparator)
	for _, f := range r.File {
		// Archives written by Windows PowerShell use backslashes.
		name := filepath.FromSlash(strings.ReplaceAll(f.Name, `\`, "/"))
		target := filepath.Join(dir, name)
		if !strings.HasPrefix(target+string(os.PathSeparator), root) {
			return fmt.Errorf("%q points outside the bundle", f.Name)
		}
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extract(f, target); err != nil {
			return err
		}
	}
	return nil
}

func extract(f *zip.File, target string) error {
	in, err := f.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("extracting %s: %w", f.Name, err)
	}
	return out.Close()
}
