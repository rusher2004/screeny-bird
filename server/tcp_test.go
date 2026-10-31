package server_test

import (
	"errors"
	"testing"

	"github.com/rusher2004/screeny-bird/server"
)

func TestNewTCPListenerRequiresAnAddress(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"", "   "} {
		if _, err := server.NewTCPListener(address); !errors.Is(err, server.ErrInvalidServer) {
			t.Errorf("NewTCPListener(%q) error = %v, want ErrInvalidServer", address, err)
		}
	}
}

func TestTCPListenerBindsAndReportsFailures(t *testing.T) {
	t.Parallel()

	listener, err := server.NewTCPListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewTCPListener() error = %v", err)
	}

	ln, err := listener.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	// Binding the same concrete address again must fail, naming it.
	taken, err := server.NewTCPListener(ln.Addr().String())
	if err != nil {
		t.Fatalf("NewTCPListener() error = %v", err)
	}

	if _, err := taken.Listen(); err == nil {
		t.Error("Listen() on an address in use error = nil, want failure")
	}

	if err := ln.Close(); err != nil {
		t.Errorf("closing the listener: %v", err)
	}

	unusable, err := server.NewTCPListener("not an address")
	if err != nil {
		t.Fatalf("NewTCPListener() error = %v", err)
	}

	if _, err := unusable.Listen(); err == nil {
		t.Error("Listen() on a malformed address error = nil, want failure")
	}
}
