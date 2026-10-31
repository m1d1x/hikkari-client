//go:build !windows

// hikkari.exe (the launcher) exists only on Windows; macOS launches the .app
// directly.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "the hikkari launcher is Windows-only")
	os.Exit(1)
}
