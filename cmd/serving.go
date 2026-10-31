package main

import (
	"fmt"
	"log/slog"
	"net/http"
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
		ReadHeaderTimeout: time.Duration(cfg.ReadHeaderTimeoutSeconds) * time.Second,
		WriteTimeout:      time.Duration(cfg.WriteTimeoutSeconds) * time.Second,
		ShutdownTimeout:   time.Duration(cfg.ShutdownTimeoutSeconds) * time.Second,
		ReloadInterval:    time.Duration(cfg.ReloadIntervalSeconds) * time.Second,
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
	client := &http.Client{
		Timeout:   time.Duration(cfg.HealthcheckTimeoutSeconds) * time.Second,
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	prober, err := server.NewProber(cfg.ListenAddress, client)
	if err != nil {
		return server.Prober{}, fmt.Errorf("creating prober: %w", err)
	}

	return prober, nil
}
