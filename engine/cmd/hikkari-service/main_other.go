//go:build !windows

// HikkariService.exe exists only on Windows; macOS has its LaunchDaemon
// (core/vpn/process_darwin.go).
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "HikkariService is Windows-only")
	os.Exit(1)
}
