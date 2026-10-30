//go:build windows || darwin

// Package svcclient talks to HikkariService over its control endpoint (a
// named pipe on Windows, a unix socket on macOS). Shared by the GUI engine
// (VPN control, update state) and the Windows launcher.
package svcclient

import (
	"errors"
	"fmt"
	"time"

	"NekkoVPN/core/svcproto"
)

// ErrNoService: the endpoint is not there — the service is not installed or not
// running.
var ErrNoService = errors.New("служба Hikkari не запущена — переустановите приложение")

// Call sends one request and returns the service's answer. A response with
// ok=false comes back as an error carrying the service's message.
func Call(req svcproto.Request, timeout time.Duration) (svcproto.Response, error) {
	conn, err := dial(2 * time.Second)
	if err != nil {
		return svcproto.Response{}, fmt.Errorf("%w (%v)", ErrNoService, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := svcproto.WriteMessage(conn, req); err != nil {
		return svcproto.Response{}, fmt.Errorf("служба Hikkari: отправка запроса: %w", err)
	}
	var resp svcproto.Response
	if err := svcproto.ReadMessage(conn, &resp); err != nil {
		return svcproto.Response{}, fmt.Errorf("служба Hikkari: ответ: %w", err)
	}
	if !resp.OK {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}
