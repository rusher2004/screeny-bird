package server

import (
	"fmt"
	"net"
	"strings"
)

// TCPListener opens a TCP socket on one address.
type TCPListener struct {
	address string
}

// NewTCPListener returns a listener for address, a host:port. A blank address
// is an error.
func NewTCPListener(address string) (TCPListener, error) {
	if strings.TrimSpace(address) == "" {
		return TCPListener{}, fmt.Errorf("creating TCP listener: address: %w", ErrInvalidServer)
	}

	return TCPListener{address: address}, nil
}

// Listen binds the address.
func (l TCPListener) Listen() (net.Listener, error) {
	ln, err := net.Listen("tcp", l.address)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", l.address, err)
	}

	return ln, nil
}
