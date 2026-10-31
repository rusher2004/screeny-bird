package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
)

// doer is the one thing a Prober needs from an HTTP client.
type doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Prober asks a running server whether it is healthy.
type Prober struct {
	url    string
	client doer
}

// NewProber returns a prober for the server that listens on listenAddress, a
// host:port. The probe runs next to the server, so a server listening on every
// interface is probed on the loopback address and one bound to a specific
// address is probed there. It reports every invalid argument together.
func NewProber(listenAddress string, client doer) (Prober, error) {
	var problems []error

	host, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		problems = append(problems, fmt.Errorf("listen address %q must look like host:port: %w", listenAddress, err))
	}

	if addr, parseErr := netip.ParseAddr(host); err == nil && (host == "" || (parseErr == nil && addr.IsUnspecified())) {
		host = "127.0.0.1"
	}

	if client == nil {
		problems = append(problems, errors.New("client is required"))
	}

	if err := errors.Join(problems...); err != nil {
		return Prober{}, fmt.Errorf("creating prober: %w: %w", ErrInvalidServer, err)
	}

	target := url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: healthPath}

	return Prober{url: target.String(), client: client}, nil
}

// Probe returns nil when the server answers 200, and an error otherwise.
func (p Prober) Probe(ctx context.Context) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, http.NoBody)
	if err != nil {
		return fmt.Errorf("building the health request for %s: %w", p.url, err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("asking %s for its health: %w", p.url, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing the health response: %w", closeErr))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d, want %d", p.url, resp.StatusCode, http.StatusOK)
	}

	return nil
}
