//go:build !windows

// HikkariSetup.exe exists only on Windows; macOS ships the .app
// directly.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "the Hikkari installer is Windows-only")
	os.Exit(1)
}
