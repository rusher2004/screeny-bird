package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/config"
	"github.com/rusher2004/screeny-bird/server"
)

// newServer builds the HTTP server over the catalog in store. The listener, the
// real clock ticker and the request logger are main's choices.
func newServer(cfg config.Config, version string, store catalog.DirLoader, logger *slog.Logger) (*server.Server, error) {
	listener, err := server.NewTCPListener(cfg.ListenAddress)
	if err != nil {
		return nil, fmt.Errorf("wiring the HTTP server: %w", err)
	}

	srv, err := server.New(server.Options{
		Store:             store,
		Listener:          listener,
		Ticker:            server.TimeTicker{},
		Log:               requestLogger{logger: logger},
		Version:           version,
		AllowedOrigins:    cfg.CORSAllowedOrigins,
		ReadHeaderTimeout: seconds(cfg.ReadHeaderTimeoutSeconds),
		WriteTimeout:      seconds(cfg.WriteTimeoutSeconds),
		ShutdownTimeout:   seconds(cfg.ShutdownTimeoutSeconds),
		ReloadInterval:    seconds(cfg.ReloadIntervalSeconds),
	})
	if err != nil {
		return nil, fmt.Errorf("wiring the HTTP server: %w", err)
	}

	return srv, nil
}

// newProber builds the prober the healthcheck command uses. Its HTTP client has
// an explicit configuration: no proxy lookup, no redirects and the configured
// timeout.
func newProber(cfg config.Config) (server.Prober, error) {
	url, err := healthURL(cfg.ListenAddress)
	if err != nil {
		return server.Prober{}, err
	}

	timeout := seconds(cfg.HealthcheckTimeoutSeconds)

	client := &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	prober, err := server.NewProber(url, client)
	if err != nil {
		return server.Prober{}, fmt.Errorf("creating prober: %w", err)
	}

	return prober, nil
}

// healthURL is where the server answers /healthz as seen from the machine it
// runs on. The healthcheck runs next to the server, so a server listening on
// every interface is probed on the loopback address; one bound to a specific
// address is probed there.
func healthURL(listenAddress string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return "", fmt.Errorf("deriving the health URL from listen_address %q: %w", listenAddress, err)
	}

	if addr, parseErr := netip.ParseAddr(host); host == "" || (parseErr == nil && addr.IsUnspecified()) {
		host = "127.0.0.1"
	}

	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}

func seconds(n int) time.Duration {
	return time.Duration(n) * time.Second
}
