//go:build darwin

package macsvc

import (
	"os"
	"os/signal"
	"syscall"
)

func signalNotify(ch chan<- os.Signal) { signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT) }
