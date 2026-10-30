//go:build windows

package ipc

import (
	"context"
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
		go serveConn(ctx, c, h)
	}
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
	return exchange(c, cmd, timeout)
}
