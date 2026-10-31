// Package server serves the validated catalog and its images over HTTP.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rusher2004/screeny-bird/catalog"
)

const (
	// maxHeaderBytes bounds request headers. It is a safety invariant, not an
	// operator setting.
	maxHeaderBytes = 8 << 10

	// schemaVersion is the catalog schema version the API serves.
	schemaVersion = 1
)

// ErrInvalidServer reports a server built without a required option or with an
// unusable one.
var ErrInvalidServer = errors.New("invalid server")

// store is what the server needs from the catalog on disk.
type store interface {
	// Load validates and returns the whole catalog.
	Load() (*catalog.Catalog, error)
	// IndexVersion returns a token that changes whenever the catalog's index
	// is replaced, so a change can be noticed without loading.
	IndexVersion() (string, error)
	// ReadAsset returns the bytes of one file inside the catalog directory.
	ReadAsset(name string) ([]byte, error)
}

// listener opens the socket the server accepts connections on.
type listener interface {
	Listen() (net.Listener, error)
}

// ticker delivers a signal every interval until stop is called.
type ticker interface {
	Tick(every time.Duration) (ticks <-chan struct{}, stop func())
}

// accessLog records one finished request.
type accessLog interface {
	Log(method, path string, status, bytes int)
}

// Options is everything a server depends on. Every field is required.
type Options struct {
	Store    store
	Listener listener
	Ticker   ticker
	Log      accessLog
	// Version is the application version /healthz reports.
	Version string
	// AllowedOrigins are the browser origins allowed to read the catalog and
	// images. An empty, non-nil list allows none.
	AllowedOrigins    []string
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	ShutdownTimeout   time.Duration
	ReloadInterval    time.Duration
}

// Server serves one catalog. Build it with New and run it with Serve.
type Server struct {
	opts    Options
	allowed map[string]bool

	// state is the catalog being served. It is replaced whole, never edited.
	state atomic.Pointer[snapshot]
	// reloadFailing is true while the latest reload failed and the server is
	// still serving the last catalog it validated.
	reloadFailing atomic.Bool
	// attempted is the index version the last reload looked at. Only the
	// serving loop touches it.
	attempted string
}

// New returns a server that has every option it needs. It reports every
// problem together.
func New(opts Options) (*Server, error) {
	var problems []error

	for _, dep := range []struct {
		name    string
		missing bool
	}{
		{"store", opts.Store == nil},
		{"listener", opts.Listener == nil},
		{"ticker", opts.Ticker == nil},
		{"log", opts.Log == nil},
		{"version", strings.TrimSpace(opts.Version) == ""},
		{"allowed origins", opts.AllowedOrigins == nil},
	} {
		if dep.missing {
			problems = append(problems, fmt.Errorf("%s is required", dep.name))
		}
	}

	for _, d := range []struct {
		name  string
		value time.Duration
	}{
		{"read header timeout", opts.ReadHeaderTimeout},
		{"write timeout", opts.WriteTimeout},
		{"shutdown timeout", opts.ShutdownTimeout},
		{"reload interval", opts.ReloadInterval},
	} {
		if d.value <= 0 {
			problems = append(problems, fmt.Errorf("%s must be positive", d.name))
		}
	}

	allowed := make(map[string]bool, len(opts.AllowedOrigins))

	for _, origin := range opts.AllowedOrigins {
		if strings.TrimSpace(origin) == "" || strings.Contains(origin, "*") {
			problems = append(problems, fmt.Errorf("allowed origin %q must be an exact origin, never a wildcard", origin))

			continue
		}

		allowed[origin] = true
	}

	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("creating server: %w: %w", ErrInvalidServer, err)
	}

	return &Server{opts: opts, allowed: allowed}, nil
}

// snapshot is one validated catalog, ready to serve.
type snapshot struct {
	indexVersion string
	species      int
	generatedAt  string
	catalogJSON  []byte
	catalogETag  string
	// assets maps each served asset path to the SHA-256 the catalog declares.
	assets map[string]string
}

// catalogResponse is the JSON body of /v1/catalog. The field order is the
// sorted key order.
type catalogResponse struct {
	GeneratedAt   *string           `json:"generated_at"`
	SchemaVersion int               `json:"schema_version"`
	Species       []catalog.Species `json:"species"`
}

// load validates the catalog and builds a snapshot of it. It reads the index
// version first, so a change that lands during the load is noticed next time.
func (s *Server) load() (*snapshot, error) {
	version, err := s.opts.Store.IndexVersion()
	if err != nil {
		return nil, fmt.Errorf("reading the index version: %w", err)
	}

	loaded, err := s.opts.Store.Load()
	if err != nil {
		return nil, fmt.Errorf("loading the catalog: %w", err)
	}

	species := loaded.Species()
	response := catalogResponse{SchemaVersion: schemaVersion, Species: species}

	if response.Species == nil {
		response.Species = []catalog.Species{}
	}

	if latest, ok := loaded.GeneratedAt(); ok {
		response.GeneratedAt = &latest
	}

	body, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encoding the catalog: %w", err)
	}

	digest := sha256.Sum256(body)
	snap := &snapshot{
		indexVersion: version,
		species:      len(species),
		catalogJSON:  body,
		catalogETag:  `"` + hex.EncodeToString(digest[:]) + `"`,
		assets:       make(map[string]string, 2*len(species)),
	}

	if response.GeneratedAt != nil {
		snap.generatedAt = *response.GeneratedAt
	}

	for _, sp := range species {
		snap.assets[sp.PortraitPath] = sp.PortraitSHA256
		snap.assets[sp.DisplayPath] = sp.DisplaySHA256
	}

	return snap, nil
}

// Serve loads and validates the catalog, then serves it until ctx is done. A
// catalog that does not validate stops the server before it listens. While
// serving, it reloads a changed catalog on every tick and keeps serving the
// last validated one if a reload fails. A clean shutdown returns nil.
func (s *Server) Serve(ctx context.Context) error {
	snap, err := s.load()
	if err != nil {
		return fmt.Errorf("preparing to serve: %w", err)
	}

	s.state.Store(snap)
	s.attempted = snap.indexVersion

	ln, err := s.opts.Listener.Listen()
	if err != nil {
		return fmt.Errorf("opening the listener: %w", err)
	}

	srv := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		WriteTimeout:      s.opts.WriteTimeout,
		IdleTimeout:       s.opts.ReadHeaderTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		// The server never prints; main owns the output.
		ErrorLog: log.New(io.Discard, "", 0),
	}

	served := make(chan error, 1)

	go func() { served <- srv.Serve(ln) }()

	ticks, stop := s.opts.Ticker.Tick(s.opts.ReloadInterval)
	defer stop()

	for {
		select {
		case <-ticks:
			s.reload()
		case err := <-served:
			return fmt.Errorf("serving: %w", err)
		case <-ctx.Done():
			return s.shutdown(ctx, srv, served)
		}
	}
}

// shutdown stops accepting, lets in-flight requests finish within the
// configured time, and reports anything that went wrong.
func (s *Server) shutdown(ctx context.Context, srv *http.Server, served <-chan error) error {
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.opts.ShutdownTimeout)
	defer cancel()

	var errs []error

	if err := srv.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("shutting down: %w", err))

		if closeErr := srv.Close(); closeErr != nil {
			errs = append(errs, fmt.Errorf("closing the server: %w", closeErr))
		}
	}

	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		errs = append(errs, fmt.Errorf("serving: %w", err))
	}

	return errors.Join(errs...)
}

// reload swaps in a changed catalog. It does nothing while the index is the
// one it last looked at, and keeps the old catalog if the new one is invalid.
func (s *Server) reload() {
	version, err := s.opts.Store.IndexVersion()
	if err != nil {
		s.reloadFailing.Store(true)

		return
	}

	if version == s.attempted {
		return
	}

	s.attempted = version

	snap, err := s.load()
	if err != nil {
		s.reloadFailing.Store(true)

		return
	}

	s.state.Store(snap)
	s.reloadFailing.Store(false)
}
