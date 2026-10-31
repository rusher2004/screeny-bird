package server_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/rusher2004/screeny-bird/server"
)

type trackedBody struct {
	io.Reader
	closed *bool
}

func (b trackedBody) Close() error {
	*b.closed = true

	return nil
}

type fakeDoer struct {
	requests []*http.Request
	status   int
	err      error
	closed   bool
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, req)

	if f.err != nil {
		return nil, f.err
	}

	return &http.Response{StatusCode: f.status, Body: trackedBody{Reader: bytes.NewReader(nil), closed: &f.closed}}, nil
}

func TestNewProberRejectsBadArguments(t *testing.T) {
	t.Parallel()

	doer := &fakeDoer{}

	for name, tt := range map[string]struct {
		address string
		client  *fakeDoer
	}{
		"empty address":                {"", doer},
		"no port":                      {"localhost", doer},
		"too many colons, no brackets": {"::1:8080", doer},
		"nil client":                   {":8080", nil},
		"both invalid":                 {"", nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var client interface {
				Do(req *http.Request) (*http.Response, error)
			}

			if tt.client != nil {
				client = tt.client
			}

			if _, err := server.NewProber(tt.address, client); !errors.Is(err, server.ErrInvalidServer) {
				t.Fatalf("NewProber(%q) error = %v, want ErrInvalidServer", tt.address, err)
			}
		})
	}
}

func TestProberTargetsTheServerFromInsideTheSameHost(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ listen, want string }{
		"all interfaces, no host": {":8080", "http://127.0.0.1:8080/healthz"},
		"all interfaces, IPv4":    {"0.0.0.0:9000", "http://127.0.0.1:9000/healthz"},
		"all interfaces, IPv6":    {"[::]:9000", "http://127.0.0.1:9000/healthz"},
		"loopback":                {"127.0.0.1:8080", "http://127.0.0.1:8080/healthz"},
		"a specific address":      {"10.1.2.3:8080", "http://10.1.2.3:8080/healthz"},
		"a specific IPv6 address": {"[fd00::1]:8080", "http://[fd00::1]:8080/healthz"},
		"a host name":             {"screeny.internal:8080", "http://screeny.internal:8080/healthz"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doer := &fakeDoer{status: http.StatusOK}

			prober, err := server.NewProber(tt.listen, doer)
			if err != nil {
				t.Fatalf("NewProber(%q) error = %v", tt.listen, err)
			}

			if err := prober.Probe(context.Background()); err != nil {
				t.Fatalf("Probe() error = %v", err)
			}

			if got := doer.requests[0].URL.String(); got != tt.want {
				t.Fatalf("probed %s, want %s", got, tt.want)
			}
		})
	}
}

func TestProbeSucceedsOnlyOnA200(t *testing.T) {
	t.Parallel()

	for status, wantErr := range map[int]bool{
		http.StatusOK:                  false,
		http.StatusServiceUnavailable:  true,
		http.StatusNotFound:            true,
		http.StatusMovedPermanently:    true,
		http.StatusInternalServerError: true,
	} {
		doer := &fakeDoer{status: status}

		prober, err := server.NewProber("127.0.0.1:8080", doer)
		if err != nil {
			t.Fatalf("NewProber() error = %v", err)
		}

		if err := prober.Probe(context.Background()); (err != nil) != wantErr {
			t.Errorf("Probe() with status %d error = %v, wantErr %t", status, err, wantErr)
		}

		if !doer.closed {
			t.Errorf("status %d: the response body was not closed", status)
		}

		if got := doer.requests[0].URL.String(); got != "http://127.0.0.1:8080/healthz" || doer.requests[0].Method != http.MethodGet {
			t.Errorf("request = %s %s, want GET of the health URL", doer.requests[0].Method, got)
		}
	}
}

func TestProbeWrapsTransportFailures(t *testing.T) {
	t.Parallel()

	cause := errors.New("connection refused")

	prober, err := server.NewProber("127.0.0.1:8080", &fakeDoer{err: cause})
	if err != nil {
		t.Fatalf("NewProber() error = %v", err)
	}

	if err := prober.Probe(context.Background()); !errors.Is(err, cause) {
		t.Fatalf("Probe() error = %v, want it to wrap %v", err, cause)
	}
}
