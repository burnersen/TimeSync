// Package source asks time servers for the current time and decides which
// answers can be trusted. It never changes the clock itself.
package source

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/beevik/ntp"
	"github.com/beevik/nts"

	"github.com/burnersen/TimeSync/internal/config"
)

// Kinds of samples.
const (
	KindNTP   = "ntp"
	KindNTS   = "nts"
	KindHTTPS = "https"
)

// Error classes, so callers can explain a failure in plain words.
const (
	ErrTimeout = "timeout" // no answer: UDP 123 blocked or server down
	ErrDNS     = "dns"     // name could not be resolved
	ErrNetwork = "network" // no route, connection refused, network down
	ErrKoD     = "kod"     // server asked us to go away (kiss-of-death)
	ErrInvalid = "invalid" // answer arrived but failed a sanity check
	ErrTLS     = "tls"     // NTS or HTTPS certificate problem
	ErrOther   = "other"
)

// The server's time must lie in this range. The lower bound is a date
// before this program was released: no correct server can be earlier.
var (
	MinPlausible = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	MaxPlausible = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// DefaultTimeout is how long one server may take to answer.
const DefaultTimeout = 5 * time.Second

// Sample is one server's answer.
type Sample struct {
	Server       string        `json:"server"`
	Kind         string        `json:"kind"`
	Address      string        `json:"address,omitempty"`
	Offset       time.Duration `json:"offset"`
	RTT          time.Duration `json:"rtt"`
	Stratum      int           `json:"stratum,omitempty"`
	RefID        string        `json:"refId,omitempty"`
	RootDistance time.Duration `json:"rootDistance,omitempty"`
	KissCode     string        `json:"kissCode,omitempty"`
	Err          string        `json:"error,omitempty"`
	ErrKind      string        `json:"errorKind,omitempty"`
}

// OK reports whether the sample can be used.
func (s Sample) OK() bool { return s.Err == "" }

// Options tune a query round.
type Options struct {
	Timeout time.Duration
	// Port overrides the NTP port (tests only).
	Port int
	// Now returns the local clock (tests only).
	Now func() time.Time
}

func (o Options) timeout() time.Duration {
	if o.Timeout <= 0 {
		return DefaultTimeout
	}
	return o.Timeout
}

// QueryAll asks all servers at the same time and returns their answers in
// the same order as the list.
func QueryAll(ctx context.Context, servers []string, opt Options) []Sample {
	out := make([]Sample, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = Query(ctx, s, opt)
		}()
	}
	wg.Wait()
	return out
}

// Query asks one server, using NTS when the entry starts with nts://.
func Query(ctx context.Context, server string, opt Options) Sample {
	addr, useNTS := config.SplitServer(server)
	if useNTS {
		return queryNTS(server, addr, opt)
	}
	return queryNTP(ctx, server, addr, opt)
}

func queryNTP(ctx context.Context, server, addr string, opt Options) Sample {
	s := Sample{Server: server, Kind: KindNTP}
	host, port := addr, 123
	if h, p, err := net.SplitHostPort(addr); err == nil {
		host = h
		fmt.Sscanf(p, "%d", &port)
	}
	if opt.Port != 0 {
		port = opt.Port
	}
	ip, err := resolve(ctx, host, opt.timeout())
	if err != nil {
		return fail(s, ErrDNS, err)
	}
	s.Address = net.JoinHostPort(ip, fmt.Sprint(port))

	qo := ntp.QueryOptions{Timeout: opt.timeout(), Version: 4}
	if opt.Now != nil {
		qo.GetSystemTime = opt.Now
	}
	r, err := ntp.QueryWithOptions(s.Address, qo)
	if err != nil {
		return fail(s, classify(err), err)
	}
	return fromResponse(s, r)
}

func queryNTS(server, addr string, opt Options) Sample {
	s := Sample{Server: server, Kind: KindNTS, Address: addr}
	session, err := nts.NewSessionWithOptions(addr, &nts.SessionOptions{
		Timeout:   opt.timeout(),
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13},
	})
	if err != nil {
		return fail(s, classify(err), fmt.Errorf("NTS key exchange: %w", err))
	}
	qo := &ntp.QueryOptions{Timeout: opt.timeout()}
	if opt.Now != nil {
		qo.GetSystemTime = opt.Now
	}
	r, err := session.QueryWithOptions(qo)
	if err != nil {
		return fail(s, classify(err), err)
	}
	return fromResponse(s, r)
}

func fromResponse(s Sample, r *ntp.Response) Sample {
	s.Offset = r.ClockOffset
	s.RTT = r.RTT
	s.Stratum = int(r.Stratum)
	s.RootDistance = r.RootDistance
	s.RefID = refID(r)
	if err := r.Validate(); err != nil {
		if errors.Is(err, ntp.ErrKissOfDeath) {
			s.KissCode = r.KissCode
			return fail(s, ErrKoD, fmt.Errorf("server refused service (kiss code %q)", r.KissCode))
		}
		return fail(s, ErrInvalid, err)
	}
	if err := CheckResponse(r); err != nil {
		return fail(s, ErrInvalid, err)
	}
	return s
}

// A zero NTP timestamp reads as 1900, or as 2036 once the library assumes
// the next era for values that would lie before 1970.
var (
	ntpEra0 = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	ntpEra1 = ntpEra0.Add(time.Duration(1<<32) * time.Second)
)

// CheckResponse adds the checks the NTP library leaves out. Each of them
// rejects an answer that the library would accept.
func CheckResponse(r *ntp.Response) error {
	if r.Version != 3 && r.Version != 4 {
		return fmt.Errorf("unsupported protocol version %d", r.Version)
	}
	if r.ReferenceTime.Equal(ntpEra0) || r.ReferenceTime.Equal(ntpEra1) {
		return errors.New("server has never been synchronized (reference time is zero)")
	}
	if r.Time.Before(MinPlausible) || !r.Time.Before(MaxPlausible) {
		return fmt.Errorf("implausible server time %s", r.Time.UTC().Format(time.RFC3339))
	}
	ts := r.Timestamps
	if ts.ServerXmit.Before(ts.ServerRecv) {
		return errors.New("server sent its answer before it received the question")
	}
	if ts.ServerXmit.Sub(ts.ServerRecv) > time.Second {
		return errors.New("server took implausibly long to answer")
	}
	raw := ts.ClientRecv.Sub(ts.ClientXmit) - ts.ServerXmit.Sub(ts.ServerRecv)
	if raw < -time.Millisecond {
		return errors.New("impossible round trip time")
	}
	return nil
}

func refID(r *ntp.Response) string {
	id := r.ReferenceID
	if r.Stratum <= 1 {
		b := []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
		return strings.TrimRight(strings.Map(func(c rune) rune {
			if c < 32 || c > 126 {
				return -1
			}
			return c
		}, string(b)), " ")
	}
	return fmt.Sprintf("%d.%d.%d.%d", byte(id>>24), byte(id>>16), byte(id>>8), byte(id))
}

// resolve looks up a host name and prefers an IPv4 address: many home
// connections announce IPv6 without routing it, which would end in a
// timeout instead of an answer.
func resolve(ctx context.Context, host string, timeout time.Duration) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return host, nil
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return "", err
	}
	if len(addrs) == 0 {
		return "", fmt.Errorf("no address for %s", host)
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return a, nil
		}
	}
	return addrs[0], nil
}

func fail(s Sample, kind string, err error) Sample {
	s.ErrKind = kind
	s.Err = err.Error()
	return s
}

func classify(err error) string {
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var invalidCert x509.CertificateInvalidError
	var netErr net.Error
	switch {
	case errors.As(err, &dnsErr):
		return ErrDNS
	case errors.As(err, &certErr), errors.As(err, &unknownAuth), errors.As(err, &invalidCert):
		return ErrTLS
	case errors.Is(err, os.ErrDeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return ErrTimeout
	case errors.As(err, &netErr) && netErr.Timeout():
		return ErrTimeout
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return ErrNetwork
	case isNetworkError(err):
		return ErrNetwork
	case errors.Is(err, ntp.ErrServerResponseMismatch), errors.Is(err, ntp.ErrInvalidMode),
		errors.Is(err, ntp.ErrInvalidTransmitTime), errors.Is(err, ntp.ErrServerTickedBackwards):
		return ErrInvalid
	}
	return ErrOther
}

func isNetworkError(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr)
}
