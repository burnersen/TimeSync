package source

import (
	"context"
	"encoding/binary"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// putTS writes t as a 64-bit NTP timestamp (era 0 or 1 alike, as on the wire).
func putTS(b []byte, t time.Time) {
	secs := uint64(t.Unix() + 2208988800)
	frac := (uint64(t.Nanosecond()) << 32) / 1e9
	binary.BigEndian.PutUint32(b[0:4], uint32(secs))
	binary.BigEndian.PutUint32(b[4:8], uint32(frac))
}

// fakeServer answers exactly one request with whatever reply builds.
func fakeServer(t *testing.T, reply func(req []byte) []byte) int {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1024)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if r := reply(buf[:n]); r != nil {
			pc.WriteTo(r, addr)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

// good builds a correct stratum 2 reply from a server whose clock is ahead
// of ours by skew.
func good(req []byte, skew time.Duration) []byte {
	r := make([]byte, 48)
	r[0] = 0<<6 | 4<<3 | 4
	r[1] = 2
	r[2] = 6
	r[3] = 0xEC
	copy(r[12:16], []byte{192, 0, 2, 1})
	copy(r[24:32], req[40:48])
	now := time.Now().Add(skew)
	putTS(r[16:24], now.Add(-30*time.Second))
	putTS(r[32:40], now)
	putTS(r[40:48], now.Add(50*time.Microsecond))
	return r
}

func ask(t *testing.T, reply func([]byte) []byte) Sample {
	port := fakeServer(t, reply)
	return Query(context.Background(), "127.0.0.1", Options{Timeout: 700 * time.Millisecond, Port: port})
}

func TestGoodAnswers(t *testing.T) {
	for _, skew := range []time.Duration{0, 250 * time.Millisecond, -3 * time.Hour} {
		s := ask(t, func(q []byte) []byte { return good(q, skew) })
		if !s.OK() {
			t.Fatalf("skew %v: rejected: %s", skew, s.Err)
		}
		if d := s.Offset - skew; d > 20*time.Millisecond || d < -20*time.Millisecond {
			t.Errorf("skew %v: measured %v", skew, s.Offset)
		}
		if s.Stratum != 2 || s.RefID != "192.0.2.1" {
			t.Errorf("stratum/refid: %d %q", s.Stratum, s.RefID)
		}
	}
}

// Every case here was accepted or mishandled by version 1.0.0.
//
// A server whose timestamps are all consistently wrong (say, all in 2036)
// cannot be caught by looking at its answer alone; that is what asking
// several servers is for (see the engine tests).
func TestBadAnswersAreRejected(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		reply   func([]byte) []byte
		errPart string
	}{
		{"kiss of death RATE", ErrKoD, func(q []byte) []byte {
			r := good(q, 0)
			r[1] = 0
			copy(r[12:16], "RATE")
			return r
		}, "RATE"},
		{"leap indicator 3", ErrInvalid, func(q []byte) []byte { r := good(q, 0); r[0] |= 3 << 6; return r }, ""},
		{"wrong mode", ErrInvalid, func(q []byte) []byte { r := good(q, 0); r[0] = r[0]&^7 | 5; return r }, ""},
		{"stratum 16", ErrInvalid, func(q []byte) []byte { r := good(q, 0); r[1] = 16; return r }, ""},
		{"spoofed originate", ErrInvalid, func(q []byte) []byte { r := good(q, 0); r[25] ^= 0xFF; return r }, ""},
		{"short packet", "", func(q []byte) []byte { return good(q, 0)[:30] }, ""},
		{"seconds zero, read as year 2036", ErrInvalid, func(q []byte) []byte {
			r := good(q, 0)
			for i := 32; i < 48; i++ {
				r[i] = 0
			}
			r[39], r[47] = 1, 2
			return r
		}, ""},
		{"server answered before it was asked", ErrInvalid, func(q []byte) []byte {
			r := good(q, 0)
			now := time.Now()
			putTS(r[32:40], now.Add(time.Hour))
			putTS(r[40:48], now)
			return r
		}, ""},
		{"huge root dispersion", ErrInvalid, func(q []byte) []byte {
			r := good(q, 0)
			binary.BigEndian.PutUint32(r[8:12], 20<<16)
			return r
		}, ""},
		{"never synchronized (reference time zero)", ErrInvalid, func(q []byte) []byte {
			r := good(q, 0)
			for i := 16; i < 24; i++ {
				r[i] = 0
			}
			return r
		}, ""},
		{"stale reference (2 days old)", ErrInvalid, func(q []byte) []byte {
			r := good(q, 0)
			putTS(r[16:24], time.Now().Add(-48*time.Hour))
			return r
		}, ""},
		{"year before release", ErrInvalid, func(q []byte) []byte {
			r := good(q, 0)
			old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			putTS(r[16:24], old.Add(-time.Minute))
			putTS(r[32:40], old)
			putTS(r[40:48], old)
			return r
		}, "implausible"},
		{"no answer", ErrTimeout, func([]byte) []byte { return nil }, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := ask(t, c.reply)
			if s.OK() {
				t.Fatalf("accepted, offset %v", s.Offset)
			}
			if c.kind != "" && s.ErrKind != c.kind {
				t.Errorf("error kind %q, want %q (%s)", s.ErrKind, c.kind, s.Err)
			}
			if c.errPart != "" && !strings.Contains(s.Err, c.errPart) {
				t.Errorf("error %q does not mention %q", s.Err, c.errPart)
			}
		})
	}
}

func TestDNSFailure(t *testing.T) {
	s := Query(context.Background(), "does-not-exist.invalid", Options{Timeout: 2 * time.Second})
	if s.OK() || s.ErrKind != ErrDNS {
		t.Fatalf("got %+v", s)
	}
}

func TestQueryAllKeepsOrder(t *testing.T) {
	p1 := fakeServer(t, func(q []byte) []byte { return good(q, 0) })
	servers := []string{"does-not-exist.invalid", "127.0.0.1:" + strconv.Itoa(p1)}
	out := QueryAll(context.Background(), servers, Options{Timeout: time.Second})
	if out[0].Server != servers[0] || out[1].Server != servers[1] {
		t.Fatalf("order lost: %v / %v", out[0].Server, out[1].Server)
	}
	if !out[1].OK() {
		t.Fatalf("second server failed: %s", out[1].Err)
	}
}
