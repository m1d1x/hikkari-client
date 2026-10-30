//go:build darwin

// HikkariService — the privileged half of the macOS client. See core/macsvc.
//
//	HikkariService             serve (launchd runs it like this, as root)
//	HikkariService install     register and start the daemon (the .pkg's postinstall)
//	HikkariService uninstall   remove Hikkari: daemon, bundle, service state (sudo)
package main

import (
	"fmt"
	"os"

	"NekkoVPN/core/macsvc"
)

func main() {
	var err error
	switch {
	case len(os.Args) == 1:
		err = macsvc.Run()
	case os.Args[1] == "install":
		err = macsvc.Install()
	case os.Args[1] == "uninstall":
		err = macsvc.Uninstall()
	default:
		fmt.Fprintln(os.Stderr, "usage: HikkariService [install | uninstall]")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "HikkariService:", err)
		os.Exit(1)
	}
}
