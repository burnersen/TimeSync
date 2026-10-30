package source

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// QueryHTTPS reads the "Date" header of web servers. It is the last resort
// when UDP port 123 is blocked (common in company networks) and is only
// accurate to about a second.
func QueryHTTPS(ctx context.Context, urls []string, opt Options) []Sample {
	out := make([]Sample, len(urls))
	done := make(chan struct{})
	for i, u := range urls {
		go func() {
			out[i] = queryHTTPS(ctx, u, opt)
			done <- struct{}{}
		}()
	}
	for range urls {
		<-done
	}
	return out
}

func queryHTTPS(ctx context.Context, url string, opt Options) Sample {
	s := Sample{Server: url, Kind: KindHTTPS}
	now := time.Now
	if opt.Now != nil {
		now = opt.Now
	}
	client := httpsClient(opt.timeout())
	ctx, cancel := context.WithTimeout(ctx, opt.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return fail(s, ErrOther, err)
	}
	req.Header.Set("User-Agent", "TimeSync (time check; https://github.com/burnersen/TimeSync)")
	t1 := now()
	resp, err := client.Do(req)
	t4 := now()
	if err != nil {
		return fail(s, classify(err), err)
	}
	resp.Body.Close()
	date, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		return fail(s, ErrInvalid, errors.New("no usable Date header"))
	}
	if date.Before(MinPlausible) || !date.Before(MaxPlausible) {
		return fail(s, ErrInvalid, fmt.Errorf("implausible server time %s", date.Format(time.RFC3339)))
	}
	// The header is cut to whole seconds, so on average the real time was
	// half a second later than what it says.
	server := date.Add(500 * time.Millisecond)
	s.RTT = t4.Sub(t1)
	s.Offset = server.Sub(t1.Add(s.RTT / 2))
	return s
}

// httpsClient checks certificates as usual, except that it ignores their
// validity period. A clock that is years off would otherwise reject every
// certificate, and fixing exactly such a clock is the point of this.
func httpsClient(timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // replaced by VerifyConnection below
		VerifyConnection:   verifyIgnoringTime,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		// A redirect still carries a Date header; there is no need to follow it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func verifyIgnoringTime(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("server sent no certificate")
	}
	leaf := cs.PeerCertificates[0]
	inter := x509.NewCertPool()
	for _, c := range cs.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	// Verify as if "now" were the middle of the leaf's validity period.
	mid := leaf.NotBefore.Add(leaf.NotAfter.Sub(leaf.NotBefore) / 2)
	_, err := leaf.Verify(x509.VerifyOptions{
		DNSName:       cs.ServerName,
		Intermediates: inter,
		CurrentTime:   mid,
	})
	return err
}
