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
		url    string
		client *fakeDoer
	}{
		"empty url":    {"", doer},
		"no scheme":    {"127.0.0.1:8080/healthz", doer},
		"ftp scheme":   {"ftp://127.0.0.1/healthz", doer},
		"no host":      {"http:///healthz", doer},
		"unparseable":  {"http://[::1", doer},
		"nil client":   {"http://127.0.0.1:8080/healthz", nil},
		"both invalid": {"", nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var client interface {
				Do(req *http.Request) (*http.Response, error)
			}

			if tt.client != nil {
				client = tt.client
			}

			if _, err := server.NewProber(tt.url, client); !errors.Is(err, server.ErrInvalidServer) {
				t.Fatalf("NewProber() error = %v, want ErrInvalidServer", err)
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

		prober, err := server.NewProber("http://127.0.0.1:8080/healthz", doer)
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

	prober, err := server.NewProber("http://127.0.0.1:8080/healthz", &fakeDoer{err: cause})
	if err != nil {
		t.Fatalf("NewProber() error = %v", err)
	}

	if err := prober.Probe(context.Background()); !errors.Is(err, cause) {
		t.Fatalf("Probe() error = %v, want it to wrap %v", err, cause)
	}
}
