//go:build !windows

// hikkari-setup.exe exists only on Windows; macOS ships the .app
// directly.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "the hikkari installer is Windows-only")
	os.Exit(1)
}
