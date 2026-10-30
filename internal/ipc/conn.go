package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"
)

// Handler answers one request.
type Handler func(ctx context.Context, req Request) Response

// maxRequest bounds what a client may send; real requests are tiny.
const maxRequest = 4096

// serveConn answers exactly one request on c: one JSON line in, one out.
func serveConn(ctx context.Context, c net.Conn, h Handler) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(SyncTimeout + 5*time.Second))
	line, err := bufio.NewReaderSize(c, maxRequest).ReadSlice('\n')
	var resp Response
	var req Request
	switch {
	case err != nil:
		resp = Response{Error: "malformed request"}
	case json.Unmarshal(line, &req) != nil:
		resp = Response{Error: "malformed request"}
	default:
		resp = h(ctx, req)
	}
	out, _ := json.Marshal(resp)
	c.Write(append(out, '\n'))
}

// exchange sends one request on c and reads the answer.
func exchange(c net.Conn, cmd string, timeout time.Duration) (Response, error) {
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
