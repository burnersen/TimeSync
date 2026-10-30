package ipc

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func roundTrip(t *testing.T, h Handler, send func(c net.Conn) (Response, error)) (Response, error) {
	t.Helper()
	server, client := net.Pipe()
	go serveConn(context.Background(), server, h)
	defer client.Close()
	return send(client)
}

func TestExchange(t *testing.T) {
	h := func(_ context.Context, req Request) Response {
		if req.Cmd == CmdStatus {
			return Response{OK: true, Status: &Status{Version: "2.0.0", IntervalMinutes: 60}}
		}
		return Response{Error: "unknown command"}
	}
	resp, err := roundTrip(t, h, func(c net.Conn) (Response, error) { return exchange(c, CmdStatus, time.Second) })
	if err != nil || resp.Status == nil || resp.Status.Version != "2.0.0" {
		t.Fatalf("status: %+v %v", resp, err)
	}
	_, err = roundTrip(t, h, func(c net.Conn) (Response, error) { return exchange(c, "explode", time.Second) })
	if err == nil || err.Error() != "unknown command" {
		t.Fatalf("unknown command: %v", err)
	}
}

func TestMalformedAndOversizedRequests(t *testing.T) {
	called := false
	h := func(context.Context, Request) Response { called = true; return Response{OK: true} }
	for _, payload := range []string{"not json\n", strings.Repeat("x", 10000) + "\n"} {
		server, client := net.Pipe()
		go serveConn(context.Background(), server, h)
		client.SetDeadline(time.Now().Add(2 * time.Second))
		go client.Write([]byte(payload))
		buf := make([]byte, 200)
		n, _ := client.Read(buf)
		client.Close()
		if !strings.Contains(string(buf[:n]), "malformed request") {
			t.Errorf("payload %.20q: got %q", payload, buf[:n])
		}
	}
	if called {
		t.Fatal("handler called for a malformed request")
	}
}
