//go:build !darwin

package hostprocess

import (
	"context"
	"fmt"
)

type Daemon struct {
	Name, Binary, Directory string
	DNSAddress              string
	Args                    []string
	Ready                   func() bool
}

func (d *Daemon) Start(context.Context) error {
	return fmt.Errorf("привилегированный процесс %s пока поддержан только на macOS", d.Name)
}
func (d *Daemon) Stop() error     { return nil }
func (d *Daemon) IsRunning() bool { return false }
