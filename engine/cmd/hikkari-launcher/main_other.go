//go:build !windows

// Hikkari.exe (the launcher) exists only on Windows; macOS launches the .app
// directly.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "the Hikkari launcher is Windows-only")
	os.Exit(1)
}
