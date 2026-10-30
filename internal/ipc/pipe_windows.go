//go:build windows

package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// pipeSDDL: SYSTEM, administrators and the service itself have full
// access; every signed-in user may read and write (send requests). Network
// logons are denied outright, and go-winio additionally rejects remote
// clients at the pipe level.
const pipeSDDL = "D:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;OW)(A;;GA;;;LS)(A;;GRGW;;;AU)"

// Handler answers one request.
type Handler func(ctx context.Context, req Request) Response

// Serve listens on the pipe until ctx ends.
func Serve(ctx context.Context, h Handler) error {
	l, err := winio.ListenPipe(PipeName, &winio.PipeConfig{SecurityDescriptor: pipeSDDL})
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go handle(ctx, c, h)
	}
}

func handle(ctx context.Context, c net.Conn, h Handler) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(SyncTimeout + 5*time.Second))
	r := bufio.NewReader(c)
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req Request
	var resp Response
	if len(line) > 4096 || json.Unmarshal(line, &req) != nil {
		resp = Response{Error: "malformed request"}
	} else {
		resp = h(ctx, req)
	}
	out, _ := json.Marshal(resp)
	c.Write(append(out, '\n'))
}

// ErrNoService means the service is not running (or not installed).
var ErrNoService = errors.New("the TimeSync service is not running")

// Call sends one request to the service and waits for the answer.
func Call(cmd string, timeout time.Duration) (Response, error) {
	d := DialTimeout
	c, err := winio.DialPipe(PipeName, &d)
	if err != nil {
		return Response{}, ErrNoService
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(timeout))
	req, _ := json.Marshal(Request{Cmd: cmd})
	if _, err := c.Write(append(req, '\n')); err != nil {
		return Response{}, err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, err
	}
	if !resp.OK && resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}
