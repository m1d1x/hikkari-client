package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestTheBundleUnpacksWithEitherSlash(t *testing.T) {
	dir := t.TempDir()
	data := archive(t, map[string]string{
		"Hikkari/HikkariService.exe": "svc",
		`Hikkari\app\VERSION`:        "1.0.0", // Windows PowerShell's Compress-Archive
	})
	if err := unzip(data, dir); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"Hikkari/HikkariService.exe": "svc",
		"Hikkari/app/VERSION":        "1.0.0",
	} {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v", path, got, err)
		}
	}
}

func TestAnEntryOutsideTheBundleIsRefused(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../evil.exe", "Hikkari/../../evil.exe", `..\evil.exe`} {
		if err := unzip(archive(t, map[string]string{name: "x"}), filepath.Join(dir, "work")); err == nil {
			t.Fatalf("%q was extracted", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.exe")); err == nil {
		t.Fatal("a file landed outside the bundle")
	}
}
