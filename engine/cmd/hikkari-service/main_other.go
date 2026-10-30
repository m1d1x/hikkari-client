//go:build !windows && !darwin

// HikkariService exists on Windows (core/winsvc) and macOS (core/macsvc).
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "HikkariService runs on Windows and macOS only")
	os.Exit(1)
}
