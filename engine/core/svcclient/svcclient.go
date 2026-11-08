//go:build windows || darwin

// Package svcclient talks to HikkariService over its control endpoint (a
// named pipe on Windows, a unix socket on macOS). Shared by the GUI engine
// (VPN control, update state) and the Windows launcher.
package svcclient

import (
	"errors"
	"fmt"
	"time"

	"NekkoVPN/core/appversion"
	"NekkoVPN/core/svcproto"
)

// ErrNoService: the endpoint is not there — the service is not installed or not
// running.
var ErrNoService = errors.New("служба hikkari не запущена: переустановите приложение")

// Call sends one request and returns the service's answer. A response with
// ok=false comes back as an error carrying the service's message.
//
// The request names the release this process was started from
// (appversion.Own) unless the caller stated one. The service compares it with
// the installation on a core start and refuses another release's
// (CodeAppReplaced), and its trial watchdog reads a GUI's contact as evidence
// of life (Server.guiAlive). Own is captured once, early, so a swap later in
// this process's life cannot change the answer.
func Call(req svcproto.Request, timeout time.Duration) (svcproto.Response, error) {
	if req.AppVersion == "" {
		req.AppVersion = appversion.Own()
	}
	conn, err := dial(2 * time.Second)
	if err != nil {
		return svcproto.Response{}, fmt.Errorf("%w (%v)", ErrNoService, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := svcproto.WriteMessage(conn, req); err != nil {
		return svcproto.Response{}, fmt.Errorf("служба hikkari: отправка запроса: %w", err)
	}
	var resp svcproto.Response
	if err := svcproto.ReadMessage(conn, &resp); err != nil {
		return svcproto.Response{}, fmt.Errorf("служба hikkari: ответ: %w", err)
	}
	if !resp.OK {
		if resp.Code != "" {
			return resp, &Refusal{Code: resp.Code, Message: resp.Error}
		}
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// Refusal is a service error with a code the caller acts on
// (svcproto.Response.Code).
type Refusal struct {
	Code    string
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// IsRefusal says err is the service's refusal with code.
func IsRefusal(err error, code string) bool {
	var r *Refusal
	return errors.As(err, &r) && r.Code == code
}
