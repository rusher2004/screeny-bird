package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

// NewProber returns a prober for the /healthz endpoint at rawURL. It reports
// every invalid argument together.
func NewProber(rawURL string, client doer) (Prober, error) {
	var problems []error

	if parsed, err := url.Parse(rawURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		problems = append(problems, fmt.Errorf("url %q must be an http or https URL with a host", rawURL))
	}

	if client == nil {
		problems = append(problems, errors.New("client is required"))
	}

	if err := errors.Join(problems...); err != nil {
		return Prober{}, fmt.Errorf("creating prober: %w: %w", ErrInvalidServer, err)
	}

	return Prober{url: rawURL, client: client}, nil
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
